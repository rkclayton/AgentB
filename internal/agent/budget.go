package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/llm"
	"harness/internal/session"
)

// ChatML-shaped fallbacks used only when a server cannot render its template.
var fallbackOverhead = map[string]int{"system": 4, "user": 4, "assistant": 4, "assistant_tools": 12, "tool": 5}

type budgetInput struct {
	SystemBase, System string
	Schemas            []any
	AllSchemas         map[string]any
	Messages           []llm.Message
	Records            []events.Message
}
type budgetState struct {
	cpt                    float64
	lastChars              float64
	pendingChars           float64
	requestEstimate        int
	measured, cached       int
	hasMeasured, hasCached bool
}
type Budgeter struct {
	mu     sync.Mutex
	states map[string]*budgetState
}

func NewBudgeter() *Budgeter { return &Budgeter{states: map[string]*budgetState{}} }
func (b *Budgeter) state(id string) *budgetState {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.states[id] == nil {
		b.states[id] = &budgetState{cpt: 3.6}
	}
	copy := *b.states[id]
	return &copy
}
func (b *Budgeter) save(id string, update func(*budgetState)) {
	b.mu.Lock()
	if b.states[id] == nil {
		b.states[id] = &budgetState{cpt: 3.6}
	}
	update(b.states[id])
	b.mu.Unlock()
}
func (b *Budgeter) RecordUsage(id string, promptTokens, cached int) {
	b.save(id, func(state *budgetState) {
		state.measured, state.hasMeasured = promptTokens, true
		if cached >= 0 {
			state.cached, state.hasCached = cached, true
		}
		if promptTokens > 0 && state.lastChars > 0 {
			observed := state.lastChars / float64(promptTokens)
			state.cpt = .5*state.cpt + .5*observed
		}
	})
}
func (b *Budgeter) MarkRequest(id string, estimate int) {
	b.save(id, func(state *budgetState) {
		state.requestEstimate = estimate
		state.lastChars = state.pendingChars
	})
}
func (b *Budgeter) Measure(ctx context.Context, profile *config.Profile, s *session.Session, global config.GlobalContext, in budgetInput, markRequest bool) (events.Budget, error) {
	state := b.state(s.ID)
	categories := map[string]int{"system": 0, "memory": 0, "tools": 0, "history": 0, "files": 0, "results": 0, "fetched": 0, "summary": 0}
	estimated := []string{}
	messageCounts := map[string]session.MessageCount{}
	schemaCounts := map[string]int{}
	mode := "exact"
	var effectiveChars float64
	forceEstimate := global.Accounting == "estimated" || !profile.Capabilities.Tokenize
	client := llm.New(profile)
	if forceEstimate {
		mode = "estimated"
		estimated = []string{"system", "memory", "tools", "history", "files", "results", "fetched", "summary"}
		cpt := state.cpt
		if cpt <= 0 {
			cpt = 3.6
		}
		baseChars := float64(len([]rune(in.SystemBase)))
		fullChars := float64(len([]rune(in.System)))
		categories["system"] = estimateChars(baseChars, cpt)
		categories["memory"] = estimateChars(math.Max(0, fullChars-baseChars), cpt)
		toolData, _ := json.Marshal(in.Schemas)
		toolChars := float64(len([]rune(string(toolData)))) * 1.1
		categories["tools"] = estimateChars(toolChars, cpt)
		effectiveChars = fullChars + toolChars
		for index, message := range in.Messages {
			chars := messageChars(message)
			tokens := estimateChars(chars, cpt)
			category := in.Records[index].Category
			categories[category] += tokens
			messageCounts[in.Records[index].ID] = session.MessageCount{Tokens: tokens, Estimated: true}
			effectiveChars += chars
		}
		for name, schema := range in.AllSchemas {
			data, _ := json.Marshal(schema)
			schemaCounts[name] = estimateChars(float64(len([]rune(string(data))))*1.1, cpt)
		}
	} else if !profile.Capabilities.ApplyTemplate {
		estimated = []string{"system", "memory", "tools", "history", "files", "results", "fetched", "summary"}
		count := func(text string) int {
			value, err := client.Tokenize(ctx, text, false)
			if err != nil {
				return int(math.Ceil(float64(len([]rune(text))) / 3.6))
			}
			return value
		}
		categories["system"] = count(in.SystemBase) + fallbackOverhead["system"]
		categories["memory"] = max(0, count(in.System)-count(in.SystemBase))
		toolData, _ := json.Marshal(in.Schemas)
		categories["tools"] = int(math.Ceil(float64(count(string(toolData))) * 1.1))
		for index, message := range in.Messages {
			overhead := fallbackOverhead[message.Role]
			if message.Role == "assistant" && len(message.ToolCalls) > 0 {
				overhead = fallbackOverhead["assistant_tools"]
			}
			tokens := count(messageText(message.Content)) + count(message.ReasoningContent) + overhead
			for _, call := range message.ToolCalls {
				tokens += count(call.Function.Arguments)
			}
			category := in.Records[index].Category
			categories[category] += tokens
			messageCounts[in.Records[index].ID] = session.MessageCount{Tokens: tokens, Estimated: true}
		}
		for name, schema := range in.AllSchemas {
			data, _ := json.Marshal(schema)
			schemaCounts[name] = int(math.Ceil(float64(count(string(data))) * 1.1))
		}
	} else {
		render := func(messages []llm.Message, tools []any) (int, error) {
			prompt, err := client.ApplyTemplate(ctx, messages, tools)
			if err != nil {
				return 0, err
			}
			return client.Tokenize(ctx, prompt, false)
		}
		base, err := render([]llm.Message{{Role: "system", Content: in.SystemBase}}, nil)
		if err != nil {
			return events.Budget{}, err
		}
		withMemory, err := render([]llm.Message{{Role: "system", Content: in.System}}, nil)
		if err != nil {
			return events.Budget{}, err
		}
		categories["system"], categories["memory"] = base, max(0, withMemory-base)
		previous := withMemory
		activeTools := []any(nil)
		if profile.Capabilities.ApplyTemplateTools {
			activeTools = in.Schemas
			withTools, err := render([]llm.Message{{Role: "system", Content: in.System}}, activeTools)
			if err != nil {
				return events.Budget{}, err
			}
			categories["tools"] = max(0, withTools-withMemory)
			previous = withTools
			for name, schema := range in.AllSchemas {
				one, err := render([]llm.Message{{Role: "system", Content: in.System}}, []any{schema})
				if err != nil {
					return events.Budget{}, fmt.Errorf("count schema %s: %w", name, err)
				}
				schemaCounts[name] = max(0, one-withMemory)
			}
		} else {
			estimated = append(estimated, "tools")
			data, _ := json.Marshal(in.Schemas)
			value, err := client.Tokenize(ctx, string(data), false)
			if err != nil {
				return events.Budget{}, fmt.Errorf("count tool schemas: %w", err)
			}
			categories["tools"] = int(math.Ceil(float64(value) * 1.1))
			previous += categories["tools"]
			for name, schema := range in.AllSchemas {
				raw, _ := json.Marshal(schema)
				value, err := client.Tokenize(ctx, string(raw), false)
				if err != nil {
					return events.Budget{}, fmt.Errorf("count schema %s: %w", name, err)
				}
				schemaCounts[name] = int(math.Ceil(float64(value) * 1.1))
			}
		}
		prefix := []llm.Message{{Role: "system", Content: in.System}}
		for index := 0; index < len(in.Messages); index++ {
			message := in.Messages[index]
			groupEnd := index
			prefix = append(prefix, message)
			if message.Role == "assistant" && len(message.ToolCalls) > 0 {
				for groupEnd+1 < len(in.Messages) && groupEnd-index < len(message.ToolCalls) && in.Messages[groupEnd+1].Role == "tool" {
					groupEnd++
					prefix = append(prefix, in.Messages[groupEnd])
				}
				if groupEnd-index < len(message.ToolCalls) {
					return events.Budget{}, fmt.Errorf("incomplete tool-call group")
				}
			}
			current, err := render(prefix, activeTools)
			if err != nil {
				return events.Budget{}, err
			}
			if !profile.Capabilities.ApplyTemplateTools {
				current += categories["tools"]
			}
			groupTokens := max(0, current-previous)
			previous = current
			weights, totalWeight := make([]int, groupEnd-index+1), 0
			for offset := range weights {
				candidate := in.Messages[index+offset]
				weight, err := client.Tokenize(ctx, messageText(candidate.Content)+candidate.ReasoningContent, false)
				if err != nil {
					return events.Budget{}, fmt.Errorf("weight message %d: %w", index+offset, err)
				}
				for _, call := range candidate.ToolCalls {
					value, err := client.Tokenize(ctx, call.Function.Arguments, false)
					if err != nil {
						return events.Budget{}, fmt.Errorf("weight tool call %s: %w", call.Function.Name, err)
					}
					weight += value
				}
				weight += 1
				weights[offset], totalWeight = weight, totalWeight+weight
			}
			assigned := 0
			for offset, weight := range weights {
				tokens := groupTokens - assigned
				if offset < len(weights)-1 {
					tokens = int(math.Round(float64(groupTokens*weight) / float64(totalWeight)))
					assigned += tokens
				}
				record := in.Records[index+offset]
				categories[record.Category] += tokens
				messageCounts[record.ID] = session.MessageCount{Tokens: tokens, Estimated: false}
			}
			index = groupEnd
		}
	}
	s.SetMessageCounts(messageCounts)
	s.SetSchemaTokens(schemaCounts)
	used := 0
	for _, value := range categories {
		used += value
	}
	nctx := profile.Capabilities.NCtx
	if profile.Context.NCtxOverride > 0 {
		nctx = profile.Context.NCtxOverride
	}
	budget := events.Budget{NCtx: nctx, Reserve: profile.Context.ReserveOutput, Ceiling: max(0, nctx-profile.Context.ReserveOutput), UsedEst: used, Mode: mode, Estimated: len(estimated) > 0, EstimatedCategories: estimated, Categories: categories}
	if state.hasMeasured {
		budget.UsedMeasured = state.measured
		budget.Drift = state.measured - state.requestEstimate
	}
	if profile.Capabilities.CachedTokens && state.hasCached {
		value := state.cached
		budget.CachedLast = &value
	}
	b.save(s.ID, func(saved *budgetState) {
		saved.pendingChars = effectiveChars
		if markRequest {
			saved.requestEstimate = used
			saved.lastChars = effectiveChars
		}
	})
	s.SetBudget(budget)
	return budget, nil
}
func estimateChars(chars, cpt float64) int {
	if chars <= 0 {
		return 0
	}
	return int(math.Ceil(chars / cpt))
}
func messageChars(message llm.Message) float64 {
	chars := len([]rune(messageText(message.Content))) + len([]rune(message.ReasoningContent))
	for _, call := range message.ToolCalls {
		chars += len([]rune(call.Function.Arguments))
	}
	return float64(chars)
}
func messageText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}
