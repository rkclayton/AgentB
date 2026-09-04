package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"harness/internal/config"
	contextmgr "harness/internal/context"
	"harness/internal/events"
	"harness/internal/llm"
	"harness/internal/session"
	"harness/internal/tools"
)

type Runner struct {
	bus     *events.Bus
	tools   *tools.Registry
	prompt  *PromptRenderer
	profile func(string) (*config.Profile, bool)
	cfg     func() config.Config
	gate    *Gate
	budget  *Budgeter
	compact *contextmgr.Compactor
	ids     atomic.Int64
}

func NewRunner(bus *events.Bus, registry *tools.Registry, prompt *PromptRenderer, profile func(string) (*config.Profile, bool), cfg func() config.Config) *Runner {
	return &Runner{bus: bus, tools: registry, prompt: prompt, profile: profile, cfg: cfg, gate: NewGate(bus, cfg), budget: NewBudgeter(), compact: contextmgr.New(bus)}
}
func (r *Runner) Configure(cfg config.Config) { r.tools.Configure(cfg) }
func (r *Runner) Gate() *Gate                 { return r.gate }
func (r *Runner) id(prefix string) string     { return fmt.Sprintf("%s-%d", prefix, r.ids.Add(1)) }
func (r *Runner) AddUser(ctx context.Context, s *session.Session, text string) (events.Message, error) {
	message, err := r.QueueUser(ctx, s, text)
	if err != nil {
		return events.Message{}, err
	}
	r.AppendUser(s, message)
	return message, nil
}
func (r *Runner) QueueUser(ctx context.Context, s *session.Session, text string) (events.Message, error) {
	profile, ok := r.profile(s.ServerID)
	if !ok {
		return events.Message{}, fmt.Errorf("profile not found")
	}
	tokens, estimated := r.count(ctx, profile, text)
	message := events.Message{ID: r.id("m"), Role: "user", Content: text, Category: "history", Tokens: tokens, Estimated: estimated}
	return message, nil
}
func (r *Runner) AppendUser(s *session.Session, message events.Message) {
	s.Append(message)
	r.bus.Publish(events.New(events.MessageAppended, s.ID, "", map[string]any{"message": message}))
}

func (r *Runner) Run(ctx context.Context, s *session.Session, runID string) (string, string, int) {
	profile, ok := r.profile(s.ServerID)
	if !ok {
		return "profile_not_runnable", "profile not found", 0
	}
	snapshot := s.Snapshot(nil)
	if !snapshot.Runnable {
		return "profile_not_runnable", snapshot.NotRunnableReason, 0
	}
	defer r.PublishBudget(context.Background(), s)
	turn := 0
	lengthSeen := false
	runCfg := r.cfg().Run
	guards := newRunGuards(runCfg.CycleWindow, runCfg.MaxConsecutiveToolErrors)
	currentReasoning := map[string]bool{}
	for {
		if ctx.Err() != nil {
			return "user_stop", "", turn
		}
		turn++
		profile, ok = r.profile(s.ServerID)
		if !ok {
			return "profile_not_runnable", "profile not found", turn - 1
		}
		client := llm.New(profile)
		state := s.Snapshot(nil).Run
		state.Turn = turn
		s.SetRun(state)
		enabled := s.EnabledTools()
		schemas := r.tools.Schemas(enabled)
		toolNames := r.tools.Names(enabled)
		var request llm.Request
		var body map[string]any
		var requestEvent events.Event
		system := ""
		var budget events.Budget
		var budgetErr error
		r.stage(s, runID, turn, "assemble", func() {
			systemBase := r.prompt.Render(profile, s, toolNames, "")
			system = r.prompt.Render(profile, s, toolNames, s.MemoryBlock)
			messages := []llm.Message{{Role: "system", Content: system}}
			records := s.MessagesCopy()
			for _, message := range records {
				converted := llm.Message{Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID, Name: message.Name}
				for _, call := range message.ToolCalls {
					converted.ToolCalls = append(converted.ToolCalls, llm.ToolCall{ID: call.ID, Type: "function", Function: llm.FunctionCall{Name: call.Name, Arguments: call.Arguments}})
				}
				if profile.Reasoning.Preserve && currentReasoning[message.ID] {
					converted.ReasoningContent = message.Reasoning
				}
				messages = append(messages, converted)
			}
			request = llm.Request{Messages: messages, Tools: schemas, ToolChoice: "auto", MaxTokens: profile.Context.ReserveOutput, Thinking: profile.Reasoning.Enabled}
			body = llm.BuildRequest(profile, request, true)
			budget, budgetErr = r.budget.Measure(ctx, profile, s, r.cfg().Context, budgetInput{SystemBase: systemBase, System: system, Schemas: schemas, AllSchemas: r.tools.AllSchemas(), Messages: messages[1:], Records: records}, false)
			if budgetErr != nil {
				return
			}
			r.bus.Publish(events.New(events.BudgetEvent, s.ID, runID, budget))
			data := map[string]any{"turn": turn, "message_count": len(messages), "tool_count": len(schemas), "params": requestParams(profile), "est_prompt_tokens": budget.UsedEst, "estimated": budget.Estimated}
			requestEvent = events.New(events.ModelRequest, s.ID, runID, data)
			requestEvent.Body = body
		})
		if budgetErr != nil {
			return "model_error", "budget accounting: " + budgetErr.Error(), turn - 1
		}
		guardUsed := budget.UsedEst
		if budget.Mode == "estimated" {
			guardUsed = int(math.Ceil(float64(guardUsed) * 1.10))
		}
		if guardUsed+budget.Reserve > budget.NCtx {
			if r.compactToFit(ctx, s, runID, profile, currentReasoning, budget) {
				turn--
				continue
			}
			return "context_ceiling", fmt.Sprintf("prompt %d tokens + reserve %d exceeds n_ctx %d after compaction", guardUsed, budget.Reserve, budget.NCtx), turn - 1
		}
		r.bus.Publish(requestEvent)
		r.budget.MarkRequest(s.ID, budget.UsedEst)
		var response llm.Response
		var callErr error
		partial := ""
		r.stage(s, runID, turn, "call_model", func() {
			response, callErr = client.ChatStream(ctx, request, func(delta llm.Delta) {
				if delta.Kind == "progress" {
					r.bus.Publish(events.New(events.ModelProgress, s.ID, runID, map[string]any{"turn": turn, "total": delta.Total, "cache": delta.Cache, "processed": delta.Processed}))
					return
				}
				if delta.Kind == "content" {
					partial += delta.Text
					s.UpdatePartial(partial)
				}
				r.bus.Publish(events.New(events.ModelDelta, s.ID, runID, map[string]any{"turn": turn, "kind": delta.Kind, "index": delta.Index, "text": delta.Text}))
			})
			s.UpdatePartial("")
		})
		if callErr != nil {
			if ctx.Err() != nil {
				return "user_stop", "", turn
			}
			return "model_error", callErr.Error(), turn
		}
		r.budget.RecordUsage(s.ID, response.Usage.PromptTokens, response.Usage.CachedTokens)
		toolCalls := make([]events.ToolCall, 0, len(response.ToolCalls))
		for _, call := range response.ToolCalls {
			toolCalls = append(toolCalls, events.ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
		}
		responseData := map[string]any{"turn": turn, "finish_reason": response.FinishReason, "content": response.Content, "reasoning_tokens": r.textTokens(ctx, profile, response.Reasoning), "tool_calls": toolCalls, "usage": map[string]any{"prompt_tokens": response.Usage.PromptTokens, "completion_tokens": response.Usage.CompletionTokens, "cached_tokens": nullable(response.Usage.CachedTokens)}, "timings": response.Timings, "duration_ms": response.DurationMS}
		responseEvent := events.New(events.ModelResponse, s.ID, runID, responseData)
		responseEvent.Raw = string(response.Raw)
		r.bus.Publish(responseEvent)
		r.stage(s, runID, turn, "parse", func() {})
		if len(toolCalls) == 0 && response.FinishReason != "tool_calls" {
			if response.FinishReason == "length" && !lengthSeen {
				lengthSeen = true
				message, _ := r.makeMessage(ctx, profile, "user", "Your output was cut off by the token limit. Continue, briefly.", "history", turn)
				s.Append(message)
				r.bus.Publish(events.New(events.MessageAppended, s.ID, runID, map[string]any{"message": message}))
				continue
			}
			if response.FinishReason == "length" {
				return "length", "model output hit the limit twice", turn
			}
			message, _ := r.makeMessage(ctx, profile, "assistant", response.Content, "history", turn)
			message.Reasoning = response.Reasoning
			currentReasoning[message.ID] = true
			s.Append(message)
			r.bus.Publish(events.New(events.MessageAppended, s.ID, runID, map[string]any{"message": message}))
			return "done", "", turn
		}
		assistant, _ := r.makeMessage(ctx, profile, "assistant", response.Content, "history", turn)
		assistant.Reasoning = response.Reasoning
		currentReasoning[assistant.ID] = true
		assistant.ToolCalls = toolCalls
		type result struct {
			call            events.ToolCall
			args            map[string]any
			argErr          error
			content         string
			ok              bool
			operatorContext bool
			ms              int64
		}
		results := []result{}
		r.stage(s, runID, turn, "dispatch", func() {
			for _, call := range toolCalls {
				var args map[string]any
				decoded, err := tools.DecodeArgs(call.Arguments)
				if err == nil {
					args = decoded
				} else {
					args = map[string]any{}
				}
				r.bus.Publish(events.New(events.ToolCallEvent, s.ID, runID, map[string]any{"turn": turn, "call_id": call.ID, "name": call.Name, "args": args}))
				results = append(results, result{call: call, args: args, argErr: err})
			}
		})
		r.stage(s, runID, turn, "execute", func() {
			for index := range results {
				item := &results[index]
				start := time.Now()
				if item.argErr != nil {
					item.content = "error: " + item.argErr.Error()
					item.ok = false
				} else {
					item.content, item.ok, item.operatorContext = r.executeTool(ctx, s, runID, item.call.ID, item.call.Name, item.args)
				}
				item.ms = time.Since(start).Milliseconds()
				r.bus.Publish(events.New(events.ToolResult, s.ID, runID, map[string]any{"turn": turn, "call_id": item.call.ID, "name": item.call.Name, "ok": item.ok, "operator_context": item.operatorContext, "ms": item.ms, "bytes": len(item.content), "tokens": r.textTokens(ctx, profile, item.content), "preview": preview(item.content)}))
			}
		})
		r.stage(s, runID, turn, "append", func() {
			s.Append(assistant)
			r.bus.Publish(events.New(events.MessageAppended, s.ID, runID, map[string]any{"message": assistant}))
			for _, item := range results {
				category := "results"
				if item.call.Name == "read_file" {
					category = "files"
				}
				message, _ := r.makeMessage(ctx, profile, "tool", item.content, category, turn)
				message.ToolCallID = item.call.ID
				message.Name = item.call.Name
				s.Append(message)
				r.bus.Publish(events.New(events.MessageAppended, s.ID, runID, map[string]any{"message": message}))
			}
		})
		for _, item := range results {
			reason, detail, prior := guards.Observe(item.call.ID, item.call.Name, item.args, item.content)
			if reason == "cycle" {
				r.bus.Publish(events.New(events.CycleDetected, s.ID, runID, map[string]any{"call_id": item.call.ID, "name": item.call.Name, "args": item.args, "prior_call_id": prior}))
			}
			if reason != "" {
				return reason, detail, turn
			}
		}
		if turn >= r.cfg().Run.MaxTurns {
			return "turn_ceiling", "maximum turns reached", turn
		}
		r.compactAfterTurn(ctx, s, runID, turn, profile, currentReasoning)
	}
}

func (r *Runner) executeTool(ctx context.Context, s *session.Session, runID, callID, name string, args map[string]any) (string, bool, bool) {
	approved, gateErr := r.gate.Wait(ctx, s, runID, callID, name, args)
	if gateErr != nil {
		return "error: call canceled", false, false
	}
	if !approved {
		return "error: call denied by user", false, false
	}
	outcome := r.tools.CallDetailed(ctx, s, name, args)
	if !outcome.OperatorOverrideAvailable {
		return outcome.Content, outcome.OK, outcome.OperatorContext
	}
	command, _ := args["command"].(string)
	path, _ := args["path"].(string)
	subject := "tool call"
	scope := "rerun this exact tool call once"
	if name == "shell" {
		subject = "command"
		scope = "rerun this exact command once"
	}
	overrideID := callID + ":operator"
	overrideArgs := map[string]any{
		"identity": "Agent_b operator (not Administrator)",
		"reason":   outcome.OperatorOverrideReason,
		"scope":    scope,
	}
	if command != "" {
		overrideArgs["command"] = command
	}
	if path != "" {
		overrideArgs["path"] = path
	}
	overrideApproved, overrideErr := r.gate.WaitRequired(ctx, s, runID, overrideID, name+".operator_override", overrideArgs)
	if overrideErr != nil {
		return outcome.Content + "\n\noperator-identity override canceled", false, false
	}
	if !overrideApproved {
		log.Printf("%s operator-identity override denied: session=%s call=%s command=%q path=%q", name, s.ID, callID, command, path)
		return outcome.Content + "\n\noperator-identity override was offered and denied by the user", false, false
	}
	log.Printf("SECURITY: %s operator-identity override approved: session=%s call=%s command=%q path=%q", name, s.ID, callID, command, path)
	overrideContent, overrideOK := r.tools.CallAsOperator(ctx, s, name, args)
	if overrideOK {
		if strings.TrimSpace(overrideContent) == "" {
			overrideContent = "the tool completed with no output"
		}
		return "operator-identity override succeeded; exact " + subject + " rerun once:\n" + overrideContent, true, false
	}
	return outcome.Content + "\n\noperator-identity override was attempted but failed:\n" + overrideContent, false, false
}

func (r *Runner) stage(s *session.Session, runID string, turn int, name string, fn func()) {
	start := time.Now()
	r.bus.Publish(events.New(events.Stage, s.ID, runID, map[string]any{"stage": name, "state": "enter", "turn": turn, "ms": 0}))
	fn()
	r.bus.Publish(events.New(events.Stage, s.ID, runID, map[string]any{"stage": name, "state": "exit", "turn": turn, "ms": time.Since(start).Milliseconds()}))
}
func (r *Runner) makeMessage(ctx context.Context, p *config.Profile, role, content, category string, turn int) (events.Message, error) {
	tokens, estimated := r.count(ctx, p, content)
	return events.Message{ID: r.id("m"), Role: role, Content: content, Category: category, Tokens: tokens, Estimated: estimated, Turn: turn}, nil
}
func (r *Runner) count(ctx context.Context, p *config.Profile, text string) (int, bool) {
	if p.Capabilities.Tokenize {
		client := llm.New(p)
		count, err := client.Tokenize(ctx, text, false)
		if err == nil {
			return count, false
		}
	}
	return int(math.Ceil(float64(len(text)) / 3.6)), true
}
func (r *Runner) textTokens(ctx context.Context, p *config.Profile, text string) int {
	value, _ := r.count(ctx, p, text)
	return value
}
func (r *Runner) PublishBudget(ctx context.Context, s *session.Session) {
	p, ok := r.profile(s.ServerID)
	if !ok {
		return
	}
	budget, err := r.measureSession(ctx, p, s, nil, false)
	if err == nil {
		r.bus.Publish(events.New(events.BudgetEvent, s.ID, "", budget))
	}
}
func (r *Runner) measureSession(ctx context.Context, p *config.Profile, s *session.Session, currentReasoning map[string]bool, mark bool) (events.Budget, error) {
	enabled := s.EnabledTools()
	toolNames := r.tools.Names(enabled)
	schemas := r.tools.Schemas(enabled)
	base := r.prompt.Render(p, s, toolNames, "")
	system := r.prompt.Render(p, s, toolNames, s.MemoryBlock)
	records := s.MessagesCopy()
	messages := make([]llm.Message, 0, len(records))
	for _, message := range records {
		converted := llm.Message{Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID, Name: message.Name}
		for _, call := range message.ToolCalls {
			converted.ToolCalls = append(converted.ToolCalls, llm.ToolCall{ID: call.ID, Type: "function", Function: llm.FunctionCall{Name: call.Name, Arguments: call.Arguments}})
		}
		if p.Reasoning.Preserve && currentReasoning != nil && currentReasoning[message.ID] {
			converted.ReasoningContent = message.Reasoning
		}
		messages = append(messages, converted)
	}
	return r.budget.Measure(ctx, p, s, r.cfg().Context, budgetInput{SystemBase: base, System: system, Schemas: schemas, AllSchemas: r.tools.AllSchemas(), Messages: messages, Records: records}, mark)
}

func (r *Runner) compactAfterTurn(ctx context.Context, s *session.Session, runID string, turn int, p *config.Profile, current map[string]bool) {
	changed := r.compact.Supersede(s, runID, turn, func(text string) (int, bool) { return r.count(ctx, p, text) })
	budget, err := r.measureSession(ctx, p, s, current, false)
	if err != nil {
		return
	}
	if budget.Ceiling > 0 && budget.UsedEst >= int(float64(budget.Ceiling)*r.cfg().Context.SoftPct) {
		did, _ := r.compact.ElideOld(s, runID, budget.UsedEst, int(float64(budget.Ceiling)*.60), func(text string) (int, bool) { return r.count(ctx, p, text) })
		changed = changed || did
		if did {
			budget, _ = r.measureSession(ctx, p, s, current, false)
		}
	}
	if budget.Ceiling > 0 && budget.UsedEst >= int(float64(budget.Ceiling)*r.cfg().Context.SummaryPct) {
		changed = r.summarize(ctx, s, runID, p) || changed
	}
	if changed {
		r.bus.Publish(events.New(events.Stage, s.ID, runID, map[string]any{"stage": "compact", "state": "enter", "turn": turn, "ms": 0}))
		r.bus.Publish(events.New(events.Stage, s.ID, runID, map[string]any{"stage": "compact", "state": "exit", "turn": turn, "ms": 0}))
		if next, err := r.measureSession(ctx, p, s, current, false); err == nil {
			r.bus.Publish(events.New(events.BudgetEvent, s.ID, runID, next))
		}
	}
}
func (r *Runner) compactToFit(ctx context.Context, s *session.Session, runID string, p *config.Profile, current map[string]bool, budget events.Budget) bool {
	changed, _ := r.compact.ElideOld(s, runID, budget.UsedEst, int(float64(budget.Ceiling)*.60), func(text string) (int, bool) { return r.count(ctx, p, text) })
	next, err := r.measureSession(ctx, p, s, current, false)
	if err == nil {
		guard := next.UsedEst
		if next.Mode == "estimated" {
			guard = int(math.Ceil(float64(guard) * 1.10))
		}
		if guard+next.Reserve > next.NCtx {
			changed = r.summarize(ctx, s, runID, p) || changed
		}
	}
	if changed {
		r.bus.Publish(events.New(events.Stage, s.ID, runID, map[string]any{"stage": "compact", "state": "enter", "turn": s.Snapshot(nil).Run.Turn, "ms": 0}))
		r.bus.Publish(events.New(events.Stage, s.ID, runID, map[string]any{"stage": "compact", "state": "exit", "turn": s.Snapshot(nil).Run.Turn, "ms": 0}))
	}
	return changed
}
func (r *Runner) summarize(ctx context.Context, s *session.Session, runID string, p *config.Profile) bool {
	records := s.MessagesCopy()
	if len(records) <= 7 {
		return false
	}
	messages := []llm.Message{{Role: "system", Content: r.prompt.Render(p, s, r.tools.Names(s.EnabledTools()), s.MemoryBlock)}}
	for _, message := range records {
		if message.Elided || (message.Category != "history" && message.Category != "summary") {
			continue
		}
		messages = append(messages, llm.Message{Role: message.Role, Content: message.Content})
	}
	messages = append(messages, llm.Message{Role: "user", Content: "Summarize the work so far for your own future reference: the task, files touched and what changed in each, decisions made, and what remains. Under 300 words. No preamble."})
	summaryProfile := *p
	summaryProfile.Sampling.Thinking.Temperature = .3
	summaryProfile.Sampling.Nonthinking.Temperature = .3
	if len(summaryProfile.Reasoning.ValidEfforts) > 0 {
		summaryProfile.Reasoning.Effort = summaryProfile.Reasoning.ValidEfforts[0]
	}
	response, err := llm.New(&summaryProfile).Chat(ctx, llm.Request{Messages: messages, MaxTokens: 800, Thinking: summaryProfile.Reasoning.Enabled})
	if err != nil {
		return false
	}
	message, _ := r.makeMessage(ctx, p, "user", "Progress note (auto-summary of earlier turns):\n"+response.Content, "summary", 0)
	if !r.compact.Summarize(s, runID, message) {
		return false
	}
	r.bus.Publish(events.New(events.MessageAppended, s.ID, runID, map[string]any{"message": message}))
	return true
}
func requestParams(p *config.Profile) map[string]any {
	s := p.Sampling.Nonthinking
	if p.Reasoning.Enabled {
		s = p.Sampling.Thinking
	}
	control := p.Reasoning.Control
	if control == "auto" {
		control = p.Capabilities.ReasoningControl
	}
	return map[string]any{"temperature": s.Temperature, "top_p": s.TopP, "top_k": s.TopK, "min_p": s.MinP, "presence_penalty": s.PresencePenalty, "repeat_penalty": s.RepeatPenalty, "max_tokens": p.Context.ReserveOutput, "reasoning": map[string]any{"control": control, "effort": p.Reasoning.Effort, "enabled": p.Reasoning.Enabled, "preserve": p.Reasoning.Preserve}}
}
func roughBodyTokens(body any) int { return int(math.Ceil(float64(jsonSize(body)) / 3.6)) }
func jsonSize(value any) int       { data, _ := json.Marshal(value); return len(data) }
func nullable(value int) any {
	if value < 0 {
		return nil
	}
	return value
}
func preview(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	if len(value) > 500 {
		return value[:500] + "…"
	}
	return value
}
func appendUnique(values []string, value string) []string {
	for _, v := range values {
		if v == value {
			return values
		}
	}
	return append(values, value)
}
