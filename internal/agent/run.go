package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"harness/internal/config"
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
	ids     atomic.Int64
}

func NewRunner(bus *events.Bus, registry *tools.Registry, prompt *PromptRenderer, profile func(string) (*config.Profile, bool), cfg func() config.Config) *Runner {
	return &Runner{bus: bus, tools: registry, prompt: prompt, profile: profile, cfg: cfg}
}
func (r *Runner) id(prefix string) string { return fmt.Sprintf("%s-%d", prefix, r.ids.Add(1)) }
func (r *Runner) AddUser(ctx context.Context, s *session.Session, text string) (events.Message, error) {
	profile, ok := r.profile(s.ServerID)
	if !ok {
		return events.Message{}, fmt.Errorf("profile not found")
	}
	tokens, estimated := r.count(ctx, profile, text)
	message := events.Message{ID: r.id("m"), Role: "user", Content: text, Category: "history", Tokens: tokens, Estimated: estimated}
	s.Append(message)
	r.bus.Publish(events.New(events.MessageAppended, s.ID, "", map[string]any{"message": message}))
	return message, nil
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
	client := llm.New(profile)
	turn := 0
	lengthSeen := false
	lastMeasured := 0
	cached := -1
	for {
		if ctx.Err() != nil {
			return "user_stop", "", turn
		}
		turn++
		state := s.Snapshot(nil).Run
		state.Turn = turn
		s.SetRun(state)
		enabled := s.EnabledTools()
		schemas := r.tools.Schemas(enabled)
		toolNames := r.tools.Names(enabled)
		var request llm.Request
		var body map[string]any
		system := ""
		r.stage(s, runID, turn, "assemble", func() {
			system = r.prompt.Render(profile, s, toolNames, "")
			messages := []llm.Message{{Role: "system", Content: system}}
			for _, message := range s.Snapshot(nil).Messages {
				converted := llm.Message{Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID, Name: message.Name}
				for _, call := range message.ToolCalls {
					converted.ToolCalls = append(converted.ToolCalls, llm.ToolCall{ID: call.ID, Type: "function", Function: llm.FunctionCall{Name: call.Name, Arguments: call.Arguments}})
				}
				if profile.Reasoning.Preserve {
					converted.ReasoningContent = message.Reasoning
				}
				messages = append(messages, converted)
			}
			request = llm.Request{Messages: messages, Tools: schemas, ToolChoice: "auto", MaxTokens: profile.Context.ReserveOutput, Thinking: profile.Reasoning.Enabled}
			body = llm.BuildRequest(profile, request, true)
			data := map[string]any{"turn": turn, "message_count": len(messages), "tool_count": len(schemas), "params": requestParams(profile), "est_prompt_tokens": roughBodyTokens(body), "estimated": true}
			event := events.New(events.ModelRequest, s.ID, runID, data)
			event.Body = body
			r.bus.Publish(event)
		})
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
		lastMeasured = response.Usage.PromptTokens
		cached = response.Usage.CachedTokens
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
			s.Append(message)
			r.bus.Publish(events.New(events.MessageAppended, s.ID, runID, map[string]any{"message": message}))
			r.publishBudget(ctx, s, profile, system, schemas, lastMeasured, cached, runID)
			return "done", "", turn
		}
		assistant, _ := r.makeMessage(ctx, profile, "assistant", response.Content, "history", turn)
		assistant.Reasoning = response.Reasoning
		assistant.ToolCalls = toolCalls
		type result struct {
			call    events.ToolCall
			args    map[string]any
			argErr  error
			content string
			ok      bool
			ms      int64
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
					item.content, item.ok = r.tools.Call(ctx, s, item.call.Name, item.args)
				}
				item.ms = time.Since(start).Milliseconds()
				r.bus.Publish(events.New(events.ToolResult, s.ID, runID, map[string]any{"turn": turn, "call_id": item.call.ID, "name": item.call.Name, "ok": item.ok, "ms": item.ms, "bytes": len(item.content), "tokens": r.textTokens(ctx, profile, item.content), "preview": preview(item.content)}))
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
			r.publishBudget(ctx, s, profile, system, schemas, lastMeasured, cached, runID)
		})
		if turn >= r.cfg().Run.MaxTurns {
			return "turn_ceiling", "maximum turns reached", turn
		}
		r.stage(s, runID, turn, "compact", func() {})
	}
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
func (r *Runner) publishBudget(ctx context.Context, s *session.Session, p *config.Profile, system string, schemas []any, measured, cached int, runID string) {
	categories := map[string]int{"system": r.textTokens(ctx, p, system), "memory": 0, "tools": int(math.Ceil(float64(jsonSize(schemas)) / 3.6 * 1.1)), "history": 0, "files": 0, "results": 0, "summary": 0}
	estimatedCategories := []string{"tools"}
	for _, message := range s.Snapshot(nil).Messages {
		categories[message.Category] += message.Tokens
		if message.Estimated {
			estimatedCategories = appendUnique(estimatedCategories, message.Category)
		}
	}
	used := 0
	for _, value := range categories {
		used += value
	}
	nctx := p.Capabilities.NCtx
	if p.Context.NCtxOverride > 0 {
		nctx = p.Context.NCtxOverride
	}
	var cachePtr *int
	if cached >= 0 {
		v := cached
		cachePtr = &v
	}
	budget := events.Budget{NCtx: nctx, Reserve: p.Context.ReserveOutput, Ceiling: nctx - p.Context.ReserveOutput, UsedEst: used, UsedMeasured: measured, Drift: measured - used, CachedLast: cachePtr, Mode: "estimated", Estimated: len(estimatedCategories) > 0, EstimatedCategories: estimatedCategories, Categories: categories}
	s.SetBudget(budget)
	r.bus.Publish(events.New(events.BudgetEvent, s.ID, runID, budget))
}
func (r *Runner) PublishBudget(ctx context.Context, s *session.Session) {
	p, ok := r.profile(s.ServerID)
	if !ok {
		return
	}
	r.publishBudget(ctx, s, p, "", r.tools.Schemas(s.EnabledTools()), s.Snapshot(nil).Budget.UsedMeasured, -1, "")
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
