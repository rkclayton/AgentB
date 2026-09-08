package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"harness/internal/config"
	contextmgr "harness/internal/context"
	"harness/internal/delivery"
	"harness/internal/events"
	"harness/internal/llm"
	"harness/internal/session"
	"harness/internal/tools"
)

type Runner struct {
	bus                *events.Bus
	tools              *tools.Registry
	prompt             *PromptRenderer
	profile            func(string) (*config.Profile, bool)
	cfg                func() config.Config
	gate               *Gate
	budget             *Budgeter
	compact            *contextmgr.Compactor
	toolActivity       func(string)
	deliver            func(*session.Session, string, []delivery.Source)
	shellGrantMu       sync.Mutex
	shellGrants        map[string][]shellRunGrant
	shellSessionGrants map[string][]shellSessionGrant
	fileGrantMu        sync.Mutex
	fileRunGrants      map[string]bool
	fileSessionGrants  map[string]string
	identityGrantMu    sync.Mutex
	identityChatGrants map[string]string
	policyGrantMu      sync.Mutex
	policyChatGrants   map[string]map[string]bool
	renameSession      func(string, string, string) error
	ids                atomic.Int64
}

func NewRunner(bus *events.Bus, registry *tools.Registry, prompt *PromptRenderer, profile func(string) (*config.Profile, bool), cfg func() config.Config) *Runner {
	return &Runner{bus: bus, tools: registry, prompt: prompt, profile: profile, cfg: cfg, gate: NewGate(bus, cfg), budget: NewBudgeter(), compact: contextmgr.New(bus)}
}
func (r *Runner) Configure(cfg config.Config) {
	r.tools.Configure(cfg)
	r.budget.InvalidateToolCosts()
}
func (r *Runner) Gate() *Gate                     { return r.gate }
func (r *Runner) SetToolActivity(fn func(string)) { r.toolActivity = fn }
func (r *Runner) SetDeliverer(fn func(*session.Session, string, []delivery.Source)) {
	r.deliver = fn
}
func (r *Runner) SetSessionRenamer(fn func(string, string, string) error) { r.renameSession = fn }
func (r *Runner) id(prefix string) string                                 { return fmt.Sprintf("%s-%d", prefix, r.ids.Add(1)) }
func (r *Runner) AddUser(ctx context.Context, s *session.Session, text string) (events.Message, error) {
	return r.AddUserAttachments(ctx, s, text, nil)
}
func (r *Runner) AddUserAttachments(ctx context.Context, s *session.Session, text string, attachments []events.Attachment) (events.Message, error) {
	message, err := r.QueueUserAttachments(ctx, s, text, attachments)
	if err != nil {
		return events.Message{}, err
	}
	r.AppendUser(s, message)
	return message, nil
}
func (r *Runner) QueueUser(ctx context.Context, s *session.Session, text string) (events.Message, error) {
	return r.QueueUserAttachments(ctx, s, text, nil)
}
func (r *Runner) QueueUserAttachments(ctx context.Context, s *session.Session, text string, attachments []events.Attachment) (events.Message, error) {
	profile, ok := r.profile(s.ServerID)
	if !ok {
		return events.Message{}, fmt.Errorf("profile not found")
	}
	message := events.Message{ID: r.id("m"), Role: "user", Content: text, Category: "history", Attachments: append([]events.Attachment(nil), attachments...)}
	tokens, estimated := r.count(ctx, profile, renderedUserText(profile, s, message))
	message.Tokens, message.Estimated = tokens, estimated
	return message, nil
}
func (r *Runner) AppendUser(s *session.Session, message events.Message) {
	s.Append(message)
	r.bus.Publish(events.New(events.MessageAppended, s.ID, "", map[string]any{"message": message}))
}

func (r *Runner) Run(ctx context.Context, s *session.Session, runID string) (string, string, int) {
	produced := map[string]delivery.Source{}
	defer func() {
		if r.deliver != nil {
			r.deliver(s, runID, delivery.SortedSources(produced))
		}
	}()
	defer r.lapseShellGrants(s, runID)
	defer r.lapseFileRunGrant(s, runID)
	profile, ok := r.profile(s.ServerID)
	if !ok {
		return "profile_not_runnable", "profile not found", 0
	}
	snapshot := s.Snapshot()
	if !snapshot.Runnable {
		return "profile_not_runnable", snapshot.NotRunnableReason, 0
	}
	defer r.PublishBudget(ctx, s)
	turn := 0
	lengthSeen := false
	accountingRepairTried := false
	runCfg := r.cfg().Run
	guards := newRunGuards(runCfg.CycleWindow, runCfg.MaxConsecutiveToolErrors)
	currentReasoning := map[string]bool{}
	for {
		if ctx.Err() != nil {
			return "user_stop", "", turn
		}
		cfg := r.cfg()
		if agent, found := cfg.Agent(s.Snapshot().AgentID); found {
			if bound, exists := cfg.Profile(agent.B); exists {
				s.ApplyAgentConfig(s.Snapshot().AgentID, *agent, *bound)
			}
		}
		turn++
		profile, ok = r.profile(s.ServerID)
		if !ok {
			return "profile_not_runnable", "profile not found", turn - 1
		}
		client := llm.New(profile)
		state := s.Snapshot().Run
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
			systemBase := r.prompt.RenderParts(profile, s, toolNames, "", "")
			systemProject := r.prompt.RenderParts(profile, s, toolNames, s.ProjectBlock, "")
			systemWorkspaceMemory := r.prompt.RenderMemoryParts(profile, s, toolNames, s.ProjectBlock, s.MemoryBlock, "")
			system = r.prompt.RenderMemoryParts(profile, s, toolNames, s.ProjectBlock, s.MemoryBlock, s.AgentMemoryBlock)
			messages := []llm.Message{{Role: "system", Content: system}}
			records := s.MessagesCopy()
			for _, message := range records {
				converted := requestMessage(profile, s, message)
				if profile.Reasoning.Preserve && currentReasoning[message.ID] {
					converted.ReasoningContent = message.Reasoning
				}
				messages = append(messages, converted)
			}
			request = llm.Request{Messages: messages, Tools: schemas, ToolChoice: "auto", Thinking: profile.Reasoning.Enabled}
			budget, budgetErr = r.budget.Measure(ctx, profile, s, r.cfg().Context, budgetInput{SystemBase: systemBase, SystemProject: systemProject, SystemWorkspaceMemory: systemWorkspaceMemory, System: system, WithoutToolSystems: r.withoutToolSystems(profile, s, enabled, s.MemoryBlock), Schemas: schemas, AllSchemas: r.tools.AllSchemas(), Messages: messages[1:], Records: records}, false)
			if budgetErr != nil {
				return
			}
			guardUsed := guardedPromptTokens(budget)
			request.MaxTokens = requestTokenLimit(profile, budget, guardUsed)
			diagnosticRequest := request
			diagnosticRequest.Messages = diagnosticMessages(request.Messages)
			body = llm.BuildRequest(profile, diagnosticRequest, true)
			r.bus.Publish(events.New(events.BudgetEvent, s.ID, runID, budget))
			data := map[string]any{"turn": turn, "message_count": len(messages), "tool_count": len(schemas), "params": requestParams(profile, request.MaxTokens), "est_prompt_tokens": budget.UsedEst, "estimated": budget.Estimated}
			requestEvent = events.New(events.ModelRequest, s.ID, runID, data)
			requestEvent.Body = body
		})
		if budgetErr != nil {
			r.operationalError(s, runID, "budget", budgetErr)
			if !accountingRepairTried && r.repairMalformedToolCall(ctx, s, runID, profile, currentReasoning) {
				accountingRepairTried = true
				turn--
				continue
			}
			return "model_error", "budget accounting: " + budgetErr.Error(), turn - 1
		}
		guardUsed := guardedPromptTokens(budget)
		if guardUsed+budget.Reserve >= budget.NCtx {
			if r.compactToFit(ctx, s, runID, profile, currentReasoning, budget) {
				turn--
				continue
			}
			return "context_ceiling", fmt.Sprintf("prompt %d tokens + reserve %d leaves no output room in n_ctx %d after compaction", guardUsed, budget.Reserve, budget.NCtx), turn - 1
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
				r.bus.Publish(events.New(events.ModelDelta, s.ID, runID, map[string]any{"turn": turn, "kind": delta.Kind, "index": delta.Index, "text": durableModelDeltaText(delta.Kind, delta.Text)}))
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
		reasoningTokens, reasoningTokensEstimated := r.count(ctx, profile, response.Reasoning)
		durableToolCalls := sanitizedToolCalls(toolCalls)
		responseData := map[string]any{"turn": turn, "finish_reason": response.FinishReason, "content": response.Content, "reasoning_tokens": reasoningTokens, "reasoning_tokens_estimated": reasoningTokensEstimated, "tool_calls": durableToolCalls, "usage": map[string]any{"prompt_tokens": response.Usage.PromptTokens, "completion_tokens": response.Usage.CompletionTokens, "cached_tokens": nullable(response.Usage.CachedTokens)}, "timings": response.Timings, "duration_ms": response.DurationMS}
		responseEvent := events.New(events.ModelResponse, s.ID, runID, responseData)
		responseEvent.Raw = redactToolCallHeaders(string(response.Raw), toolCalls)
		s.RecordModelTurn()
		r.bus.Publish(responseEvent)
		r.maybeAutoRename(ctx, s)
		r.stage(s, runID, turn, "parse", func() {})
		if response.FinishReason == "length" && len(toolCalls) > 0 {
			r.appendTruncatedToolReply(ctx, s, runID, profile, turn, request.MaxTokens, response.Content, response.Reasoning, durableToolCalls, currentReasoning)
			if lengthSeen {
				return "length", "model output hit the limit twice", turn
			}
			lengthSeen = true
			continue
		}
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
		assistant.ToolCalls = durableToolCalls
		type result struct {
			call            events.ToolCall
			args            map[string]any
			argErr          error
			content         string
			ok              bool
			operatorContext bool
			category        string
			untrusted       bool
			metadata        map[string]any
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
				r.bus.Publish(events.New(events.ToolCallEvent, s.ID, runID, map[string]any{"turn": turn, "call_id": call.ID, "name": call.Name, "args": sanitizedToolArguments(call.Name, args)}))
				results = append(results, result{call: call, args: args, argErr: err})
			}
		})
		r.stage(s, runID, turn, "execute", func() {
			remainingResultTokens := max(0, budget.NCtx-budget.Reserve-budget.UsedEst-toolResultContextMargin)
			for index := range results {
				item := &results[index]
				start := time.Now()
				if item.argErr != nil {
					item.content = "error: " + item.argErr.Error()
					item.ok = false
				} else {
					outcome := r.executeTool(ctx, s, runID, item.call.ID, item.call.Name, item.args)
					item.content, item.ok, item.operatorContext = outcome.Content, outcome.OK, outcome.OperatorContext
					item.category, item.untrusted, item.metadata = outcome.Category, outcome.Untrusted, outcome.Metadata
					if item.ok && item.call.Name == "read_file" && untrustedAttachmentRead(s, item.args) {
						item.untrusted = true
					}
					if item.ok {
						fileMetadata := producedFileMetadata(s, item.call.Name, item.args)
						item.metadata = mergeResultMetadata(item.metadata, fileMetadata)
						if file, ok := fileMetadata["file"].(map[string]any); ok {
							path, _ := file["path"].(string)
							bytes, _ := file["bytes"].(int64)
							produced[strings.ToLower(path)] = delivery.Source{Path: path, Bytes: bytes}
						}
					}
				}
				if ctx.Err() != nil {
					break
				}
				item.ms = time.Since(start).Milliseconds()
				resultTokens := r.textTokens(ctx, profile, item.content)
				item.content, item.ok, item.metadata, resultTokens = r.fitWindowResult(
					ctx, profile, item.call.Name, item.args, item.content, item.ok, item.metadata, resultTokens, remainingResultTokens,
				)
				remainingResultTokens = max(0, remainingResultTokens-resultTokens)
				data := toolResultEventData(turn, item.call.ID, item.call.Name, item.content, item.ok, item.operatorContext, item.untrusted, item.ms, resultTokens, item.metadata)
				s.IncrementToolCall(item.call.Name)
				r.bus.Publish(events.New(events.ToolResult, s.ID, runID, data))
			}
		})
		if ctx.Err() != nil {
			return "user_stop", "", turn
		}
		r.stage(s, runID, turn, "append", func() {
			s.Append(assistant)
			r.bus.Publish(events.New(events.MessageAppended, s.ID, runID, map[string]any{"message": assistant}))
			for _, item := range results {
				category := item.category
				if category == "" {
					category = "results"
				}
				if item.call.Name == "read_file" && item.category == "" {
					category = "files"
				}
				message, _ := r.makeMessage(ctx, profile, "tool", item.content, category, turn)
				message.ToolCallID = item.call.ID
				message.Name = item.call.Name
				message.OK = boolPointer(item.ok)
				s.Append(message)
				r.bus.Publish(events.New(events.MessageAppended, s.ID, runID, map[string]any{"message": message}))
			}
		})
		for _, item := range results {
			eventArgs := sanitizedToolArguments(item.call.Name, item.args)
			reason, detail, prior := guards.Observe(item.call.ID, item.call.Name, eventArgs, item.content, item.ok)
			if reason == "cycle" {
				r.bus.Publish(events.New(events.CycleDetected, s.ID, runID, map[string]any{"call_id": item.call.ID, "name": item.call.Name, "args": eventArgs, "prior_call_id": prior}))
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

func producedFileMetadata(s *session.Session, name string, args map[string]any) map[string]any {
	if name != "write_file" && name != "edit_file" {
		return nil
	}
	requested, _ := args["path"].(string)
	if requested == "" {
		return nil
	}
	resolved, err := tools.Resolve(s.Workspace, requested)
	if err != nil {
		return nil
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	root, err := filepath.EvalSymlinks(s.Workspace)
	if err != nil {
		return nil
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil
	}
	return map[string]any{"file": map[string]any{
		"path":  filepath.ToSlash(relative),
		"bytes": info.Size(),
	}}
}

func mergeResultMetadata(current, added map[string]any) map[string]any {
	if len(added) == 0 {
		return current
	}
	if current == nil {
		current = map[string]any{}
	}
	for key, value := range added {
		current[key] = value
	}
	return current
}

func toolResultEventData(turn int, callID, name, content string, ok, operatorContext, untrusted bool, ms int64, tokens int, metadata map[string]any) map[string]any {
	data := map[string]any{
		"turn": turn, "call_id": callID, "name": name, "ok": ok,
		"operator_context": operatorContext, "untrusted": untrusted, "ms": ms,
		"bytes": len(content), "tokens": tokens, "preview": preview(content),
	}
	for key, value := range metadata {
		data[key] = value
	}
	return data
}

func (r *Runner) executeTool(ctx context.Context, s *session.Session, runID, callID, name string, args map[string]any) tools.CallOutcome {
	cfg := r.cfg()
	eventArgs := sanitizedToolArguments(name, args)
	if cfg.Shell.ServiceAccount.Enabled && !cfg.Shell.OperatorContext && r.hasIdentityChatGrant(s.ID) {
		if fileGrantTool(name) {
			return r.callFileAsOperator(ctx, s, name, args)
		}
		if name == "shell" || name == "run_script" {
			return r.callShellAsOperator(ctx, s, name, args)
		}
	}
	if fileGrantTool(name) && cfg.Shell.ServiceAccount.Enabled && !cfg.Shell.OperatorContext && r.hasFileGrant(s.ID, runID) {
		return r.callFileAsOperator(ctx, s, name, args)
	}
	if name == "shell" && cfg.Shell.ServiceAccount.Enabled && !cfg.Shell.OperatorContext {
		if r.hasShellGrant(s.ID, runID, shellGrantBoundary, "") {
			return r.callShellAsOperator(ctx, s, name, args)
		}
		if command, matched := r.tools.OperatorCommandWith(name, args, s.Policy().Shell.OperatorCommandsAdd); matched {
			return r.executeOperatorCommand(ctx, s, runID, callID, name, args, command)
		}
	}
	if name == "shell" && repoRunGrantMatches(s.Policy().Shell.RunGrantDefaults, args) && !r.hasShellGrant(s.ID, runID, shellGrantPolicy, "") {
		identity := "operator"
		if cfg.Shell.ServiceAccount.Enabled {
			identity = "service"
		}
		r.grantShellRun(s, runID, shellRunGrant{Rule: shellGrantPolicy, Identity: identity})
	}
	decision := "approve"
	var gateErr error
	if name == "run_script" && cfg.Shell.ServiceAccount.Enabled {
		var approved bool
		approved, gateErr = r.gate.WaitPolicyRequired(ctx, s, runID, callID, name, eventArgs)
		if !approved {
			decision = "deny"
		}
	} else if name == "shell" && r.hasShellGrant(s.ID, runID, shellGrantPolicy, "") {
		// An operator-approved repository default grants this displayed command
		// pattern for the run under the already-configured identity.
	} else if name == "shell" && cfg.Shell.ServiceAccount.Enabled && !cfg.Shell.OperatorContext && r.gate.requiredFor(s, name) {
		if !r.hasShellGrant(s.ID, runID, shellGrantPolicy, "") {
			decision, gateErr = r.gate.WaitPolicyDecision(ctx, s, runID, callID, name, eventArgs)
		}
	} else if r.hasPolicyChatGrant(s.ID, name) {
		// The operator already allowed this policy-governed action for the chat.
	} else {
		var approved bool
		approved, gateErr = r.gate.Wait(ctx, s, runID, callID, name, eventArgs)
		if !approved {
			decision = "deny"
		}
	}
	if gateErr != nil {
		return tools.CallOutcome{Content: "error: call canceled"}
	}
	if !approvalGranted(decision) {
		return tools.CallOutcome{Content: "error: call denied by user"}
	}
	if name == "shell" && decision == "run" {
		r.grantShellRun(s, runID, shellRunGrant{Rule: shellGrantPolicy, Identity: "service"})
	}
	if name == "shell" && decision == "session" {
		r.grantShellSession(s, runID, shellRunGrant{Rule: shellGrantPolicy, Identity: "service"})
	}
	if decision == "session" {
		r.grantPolicyChat(s.ID, name)
	}
	if name == "shell" && repoRunGrantMatches(s.Policy().Shell.RunGrantDefaults, args) {
		args = resolveRepoGrantedExecutable(cfg, args)
	}
	outcome := r.callDetailed(ctx, s, name, args)
	if !outcome.OperatorOverrideAvailable {
		return outcome
	}
	command, _ := args["command"].(string)
	path, _ := args["path"].(string)
	subject := "tool call"
	scope := "rerun this exact tool call once"
	if name == "shell" || name == "run_script" {
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
	overrideDecision := "deny"
	var overrideErr error
	if name == "shell" {
		overrideDecision, overrideErr = r.gate.WaitBoundaryDecision(ctx, s, runID, overrideID, name+".operator_override", overrideArgs)
	} else {
		overrideDecision, overrideErr = r.gate.WaitBoundaryDecision(ctx, s, runID, overrideID, name+".operator_override", overrideArgs)
	}
	if overrideErr != nil {
		outcome.Content, outcome.OK, outcome.OperatorContext = outcome.Content+"\n\noperator-identity override canceled", false, false
		return outcome
	}
	if !approvalGranted(overrideDecision) {
		log.Printf("%s operator-identity override denied: session=%s call=%s command=%q path=%q", name, s.ID, callID, command, path)
		outcome.Content, outcome.OK, outcome.OperatorContext = outcome.Content+"\n\noperator-identity override was offered and denied by the user", false, false
		return outcome
	}
	if name == "shell" && overrideDecision == "run" {
		r.grantShellRun(s, runID, shellRunGrant{Rule: shellGrantBoundary, Identity: "operator"})
	}
	if name == "shell" && overrideDecision == "session" {
		r.grantShellSession(s, runID, shellRunGrant{Rule: shellGrantBoundary, Identity: "operator"})
	}
	if fileGrantTool(name) && overrideDecision == "run" {
		r.grantFileRun(s, runID)
	}
	if fileGrantTool(name) && overrideDecision == "session" {
		r.grantFileSession(s, runID)
	}
	if overrideDecision == "session" {
		r.grantIdentityChat(s.ID, runID)
	}
	log.Printf("SECURITY: %s operator-identity override approved: session=%s call=%s command=%q path=%q", name, s.ID, callID, command, path)
	overrideContent, overrideOK := r.tools.CallAsOperator(ctx, s, name, args)
	if overrideOK {
		if strings.TrimSpace(overrideContent) == "" {
			overrideContent = "the tool completed with no output"
		}
		outcome.Content, outcome.OK, outcome.OperatorContext = "operator-identity override succeeded; exact "+subject+" rerun once:\n"+overrideContent, true, true
		return outcome
	}
	outcome.Content, outcome.OK, outcome.OperatorContext = outcome.Content+"\n\noperator-identity override was attempted but failed:\n"+overrideContent, false, true
	return outcome
}

func repoRunGrantMatches(defaults []string, args map[string]any) bool {
	command, _ := args["command"].(string)
	normalized := strings.Join(strings.Fields(strings.ToLower(command)), " ")
	for _, value := range defaults {
		candidate := strings.Join(strings.Fields(strings.ToLower(value)), " ")
		if candidate != "" && (normalized == candidate || strings.HasPrefix(normalized, candidate+" ")) {
			return true
		}
	}
	return false
}

func resolveRepoGrantedExecutable(cfg config.Config, args map[string]any) map[string]any {
	if len(cfg.Shell.Command) == 0 {
		return args
	}
	shellName := strings.ToLower(filepath.Base(cfg.Shell.Command[0]))
	if shellName != "powershell" && shellName != "powershell.exe" && shellName != "pwsh" && shellName != "pwsh.exe" {
		return args
	}
	command, _ := args["command"].(string)
	fields := strings.Fields(command)
	if len(fields) == 0 || strings.ContainsAny(fields[0], `'"&|;$(){}[]`) {
		return args
	}
	resolved, err := exec.LookPath(fields[0])
	if err != nil {
		return args
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return args
	}
	rest := strings.TrimSpace(strings.TrimPrefix(command, fields[0]))
	quoted := "'" + strings.ReplaceAll(absolute, "'", "''") + "'"
	prepared := "& " + quoted
	if rest != "" {
		prepared += " " + rest
	}
	copy := make(map[string]any, len(args))
	for key, value := range args {
		copy[key] = value
	}
	copy["command"] = prepared
	return copy
}

func (r *Runner) callFileAsOperator(ctx context.Context, s *session.Session, name string, args map[string]any) tools.CallOutcome {
	if r.toolActivity != nil {
		r.toolActivity("started")
		defer r.toolActivity("completed")
	}
	content, ok := r.tools.CallAsOperator(ctx, s, name, args)
	if ok && strings.TrimSpace(content) == "" {
		content = "the tool completed with no output"
	}
	return tools.CallOutcome{Content: content, OK: ok, OperatorContext: true}
}

func (r *Runner) callDetailed(ctx context.Context, s *session.Session, name string, args map[string]any) (outcome tools.CallOutcome) {
	if r.toolActivity != nil {
		r.toolActivity("started")
		defer r.toolActivity("completed")
	}
	return r.tools.CallDetailed(ctx, s, name, args)
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
	return int(math.Ceil(float64(len([]rune(text))) / 3.6)), true
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
	if err != nil {
		r.operationalError(s, "", "budget", err)
		return
	}
	r.bus.Publish(events.New(events.BudgetEvent, s.ID, "", budget))
}
func (r *Runner) measureSession(ctx context.Context, p *config.Profile, s *session.Session, currentReasoning map[string]bool, mark bool) (events.Budget, error) {
	enabled := s.EnabledTools()
	toolNames := r.tools.Names(enabled)
	schemas := r.tools.Schemas(enabled)
	base := r.prompt.RenderParts(p, s, toolNames, "", "")
	project := r.prompt.RenderParts(p, s, toolNames, s.ProjectBlock, "")
	workspaceMemory := r.prompt.RenderMemoryParts(p, s, toolNames, s.ProjectBlock, s.MemoryBlock, "")
	system := r.prompt.RenderMemoryParts(p, s, toolNames, s.ProjectBlock, s.MemoryBlock, s.AgentMemoryBlock)
	records := s.MessagesCopy()
	messages := make([]llm.Message, 0, len(records))
	for _, message := range records {
		converted := requestMessage(p, s, message)
		if p.Reasoning.Preserve && currentReasoning != nil && currentReasoning[message.ID] {
			converted.ReasoningContent = message.Reasoning
		}
		messages = append(messages, converted)
	}
	return r.budget.Measure(ctx, p, s, r.cfg().Context, budgetInput{SystemBase: base, SystemProject: project, SystemWorkspaceMemory: workspaceMemory, System: system, WithoutToolSystems: r.withoutToolSystems(p, s, enabled, s.MemoryBlock), Schemas: schemas, AllSchemas: r.tools.AllSchemas(), Messages: messages, Records: records}, mark)
}

func (r *Runner) withoutToolSystems(p *config.Profile, s *session.Session, enabled map[string]bool, memory string) map[string]string {
	out := map[string]string{}
	for _, name := range r.tools.Names(enabled) {
		without := make(map[string]bool, len(enabled))
		for candidate, value := range enabled {
			without[candidate] = value
		}
		without[name] = false
		out[name] = r.prompt.Render(p, s, r.tools.Names(without), memory)
	}
	return out
}

func (r *Runner) compactAfterTurn(ctx context.Context, s *session.Session, runID string, turn int, p *config.Profile, current map[string]bool) {
	cfg := r.cfg()
	readDefaultLimit := min(cfg.Tools.ReadFile.DefaultLimit, cfg.Tools.ReadFile.MaxLimit)
	changed := r.compact.Supersede(s, runID, turn, readDefaultLimit, func(text string) (int, bool) { return r.count(ctx, p, text) })
	budget, err := r.measureSession(ctx, p, s, current, false)
	if err != nil {
		r.operationalError(s, runID, "compaction_budget", err)
		return
	}
	if budget.Ceiling > 0 && budget.UsedEst >= int(float64(budget.Ceiling)*cfg.Context.SoftPct) {
		did, _ := r.compact.ElideOld(s, runID, budget.UsedEst, int(float64(budget.Ceiling)*.60), readDefaultLimit, func(text string) (int, bool) { return r.count(ctx, p, text) })
		changed = changed || did
		if did {
			budget, err = r.measureSession(ctx, p, s, current, false)
			if err != nil {
				r.operationalError(s, runID, "compaction_budget", err)
				return
			}
		}
	}
	if budget.Ceiling > 0 && budget.UsedEst >= int(float64(budget.Ceiling)*cfg.Context.SummaryPct) {
		changed = r.summarize(ctx, s, runID, p) || changed
	}
	if changed {
		r.bus.Publish(events.New(events.Stage, s.ID, runID, map[string]any{"stage": "compact", "state": "enter", "turn": turn, "ms": 0}))
		r.bus.Publish(events.New(events.Stage, s.ID, runID, map[string]any{"stage": "compact", "state": "exit", "turn": turn, "ms": 0}))
		next, err := r.measureSession(ctx, p, s, current, false)
		if err != nil {
			r.operationalError(s, runID, "compaction_budget", err)
			return
		}
		r.bus.Publish(events.New(events.BudgetEvent, s.ID, runID, next))
	}
}
func (r *Runner) compactToFit(ctx context.Context, s *session.Session, runID string, p *config.Profile, current map[string]bool, budget events.Budget) bool {
	cfg := r.cfg()
	readDefaultLimit := min(cfg.Tools.ReadFile.DefaultLimit, cfg.Tools.ReadFile.MaxLimit)
	changed, _ := r.compact.ElideOld(s, runID, budget.UsedEst, int(float64(budget.Ceiling)*.60), readDefaultLimit, func(text string) (int, bool) { return r.count(ctx, p, text) })
	next, err := r.measureSession(ctx, p, s, current, false)
	if err != nil {
		r.operationalError(s, runID, "compaction_budget", err)
	} else {
		guard := next.UsedEst
		if next.Mode == "estimated" {
			guard = int(math.Ceil(float64(guard) * 1.10))
		}
		if guard+next.Reserve > next.NCtx {
			changed = r.summarize(ctx, s, runID, p) || changed
		}
	}
	if changed {
		r.bus.Publish(events.New(events.Stage, s.ID, runID, map[string]any{"stage": "compact", "state": "enter", "turn": s.Snapshot().Run.Turn, "ms": 0}))
		r.bus.Publish(events.New(events.Stage, s.ID, runID, map[string]any{"stage": "compact", "state": "exit", "turn": s.Snapshot().Run.Turn, "ms": 0}))
	}
	return changed
}
func (r *Runner) operationalError(s *session.Session, runID, where string, err error) {
	r.bus.Publish(events.New(events.Error, s.ID, runID, map[string]any{"where": where, "message": err.Error()}))
}
func requestParams(p *config.Profile, maxTokens int) map[string]any {
	s := p.Sampling.Nonthinking
	if p.Reasoning.Enabled {
		s = p.Sampling.Thinking
	}
	control := p.Reasoning.Control
	if control == "auto" {
		control = p.Capabilities.ReasoningControl
	}
	return map[string]any{"temperature": s.Temperature, "top_p": s.TopP, "top_k": s.TopK, "min_p": s.MinP, "presence_penalty": s.PresencePenalty, "repeat_penalty": s.RepeatPenalty, "max_tokens": maxTokens, "reasoning": map[string]any{"control": control, "effort": p.Reasoning.Effort, "enabled": p.Reasoning.Enabled, "preserve": p.Reasoning.Preserve}}
}

func guardedPromptTokens(budget events.Budget) int {
	used := budget.UsedEst
	if budget.Mode == "estimated" {
		used = int(math.Ceil(float64(used) * 1.10))
	}
	return used
}

func requestTokenLimit(p *config.Profile, budget events.Budget, promptTokens int) int {
	return max(0, min(p.Context.NCtx, budget.NCtx-promptTokens-budget.Reserve))
}
func roughBodyTokens(body any) int { return int(math.Ceil(float64(jsonSize(body)) / 3.6)) }
func jsonSize(value any) int       { data, _ := json.Marshal(value); return len(data) }
func nullable(value int) any {
	if value < 0 {
		return nil
	}
	return value
}
func boolPointer(value bool) *bool { return &value }
func preview(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	runes := []rune(value)
	if len(runes) > 500 {
		return string(runes[:500]) + "…"
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
