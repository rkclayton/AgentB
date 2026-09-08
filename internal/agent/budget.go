package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/llm"
	"harness/internal/session"
)

// ChatML-shaped fallbacks used only when a server cannot render its template.
var fallbackOverhead = map[string]int{"system": 4, "user": 4, "assistant": 4, "assistant_tools": 12, "tool": 5}

type budgetInput struct {
	SystemBase, SystemProject, SystemWorkspaceMemory, System string
	WithoutToolSystems                                       map[string]string
	Schemas                                                  []any
	AllSchemas                                               map[string]any
	Messages                                                 []llm.Message
	Records                                                  []events.Message
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
	mu             sync.Mutex
	states         map[string]*budgetState
	toolCosts      map[string]cachedToolCosts
	messageWeights map[string]map[string]cachedMessageWeight
}

type cachedToolCosts struct {
	key      string
	schema   map[string]int
	marginal map[string]int
}

type cachedMessageWeight struct {
	key    string
	tokens int
}

func NewBudgeter() *Budgeter {
	return &Budgeter{states: map[string]*budgetState{}, toolCosts: map[string]cachedToolCosts{}, messageWeights: map[string]map[string]cachedMessageWeight{}}
}
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
func (b *Budgeter) ColdPrefill(id string) bool {
	state := b.state(id)
	return state.hasMeasured && state.hasCached && state.measured > 0 && state.cached < state.measured/2
}
func (b *Budgeter) InvalidateToolCosts() {
	b.mu.Lock()
	b.toolCosts = map[string]cachedToolCosts{}
	b.mu.Unlock()
}
func (b *Budgeter) cachedCosts(id, key string) (map[string]int, map[string]int, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.toolCosts[id]
	if !ok || entry.key != key {
		return nil, nil, false
	}
	return cloneCounts(entry.schema), cloneCounts(entry.marginal), true
}
func (b *Budgeter) saveCosts(id, key string, schema, marginal map[string]int) {
	b.mu.Lock()
	b.toolCosts[id] = cachedToolCosts{key: key, schema: cloneCounts(schema), marginal: cloneCounts(marginal)}
	b.mu.Unlock()
}
func (b *Budgeter) cachedMessageWeight(sessionID, messageID, key string) (int, bool) {
	if messageID == "" {
		return 0, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.messageWeights[sessionID][messageID]
	return entry.tokens, ok && entry.key == key
}
func (b *Budgeter) saveMessageWeight(sessionID, messageID, key string, tokens int) {
	if messageID == "" {
		return
	}
	b.mu.Lock()
	if b.messageWeights[sessionID] == nil {
		b.messageWeights[sessionID] = map[string]cachedMessageWeight{}
	}
	b.messageWeights[sessionID][messageID] = cachedMessageWeight{key: key, tokens: tokens}
	b.mu.Unlock()
}
func (b *Budgeter) Measure(ctx context.Context, profile *config.Profile, s *session.Session, global config.GlobalContext, in budgetInput, markRequest bool) (events.Budget, error) {
	return b.measure(ctx, profile, s, global, in, markRequest)
}

func (b *Budgeter) MeasureWithBusy(ctx context.Context, profile *config.Profile, s *session.Session, global config.GlobalContext, in budgetInput, markRequest bool, onBusy func(error)) (events.Budget, error) {
	result, err := b.measure(ctx, profile, s, global, in, markRequest)
	if err == nil || ctx.Err() != nil || llm.TransportKindOf(err) != llm.TransportConnected {
		return result, err
	}
	if onBusy != nil {
		onBusy(err)
	}
	return result, err
}

func (b *Budgeter) measure(ctx context.Context, profile *config.Profile, s *session.Session, global config.GlobalContext, in budgetInput, markRequest bool) (events.Budget, error) {
	if in.SystemProject == "" {
		in.SystemProject = in.SystemBase
	}
	if in.SystemWorkspaceMemory == "" {
		in.SystemWorkspaceMemory = in.SystemProject
	}
	state := b.state(s.ID)
	categories := map[string]int{"system": 0, "project": 0, "workspace_memory": 0, "agent_memory": 0, "tools": 0, "history": 0, "files": 0, "results": 0, "fetched": 0, "summary": 0}
	estimated := []string{}
	messageCounts := map[string]session.MessageCount{}
	forceEstimate := global.Accounting == "estimated" || !profile.Capabilities.Tokenize
	cacheCPT := 0.0
	if forceEstimate {
		cacheCPT = state.cpt
	}
	cacheKey := toolCostKey(profile, global, cacheCPT, in)
	schemaCounts, marginalCounts, costsCached := b.cachedCosts(s.ID, cacheKey)
	if !costsCached {
		schemaCounts, marginalCounts = map[string]int{}, map[string]int{}
	}
	mode := "exact"
	var effectiveChars float64
	client := llm.New(profile)
	if forceEstimate {
		mode = "estimated"
		estimated = []string{"system", "project", "workspace_memory", "agent_memory", "tools", "history", "files", "results", "fetched", "summary"}
		cpt := state.cpt
		if cpt <= 0 {
			cpt = 3.6
		}
		baseChars := float64(len([]rune(in.SystemBase)))
		projectChars := float64(len([]rune(in.SystemProject)))
		workspaceMemoryChars := float64(len([]rune(in.SystemWorkspaceMemory)))
		fullChars := float64(len([]rune(in.System)))
		categories["system"] = estimateChars(baseChars, cpt)
		categories["project"] = estimateChars(math.Max(0, projectChars-baseChars), cpt)
		categories["workspace_memory"] = estimateChars(math.Max(0, workspaceMemoryChars-projectChars), cpt)
		categories["agent_memory"] = estimateChars(math.Max(0, fullChars-workspaceMemoryChars), cpt)
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
		if !costsCached {
			for name, schema := range in.AllSchemas {
				data, _ := json.Marshal(schema)
				schemaCounts[name] = estimateChars(float64(len([]rune(string(data))))*1.1, cpt)
			}
			fullPrefix := estimateChars(fullChars+toolChars, cpt)
			for name := range in.WithoutToolSystems {
				withoutData, _ := json.Marshal(schemasWithout(in.Schemas, name))
				withoutChars := float64(len([]rune(in.WithoutToolSystems[name]))) + float64(len([]rune(string(withoutData))))*1.1
				marginalCounts[name] = max(0, fullPrefix-estimateChars(withoutChars, cpt))
			}
		}
	} else if !profile.Capabilities.ApplyTemplate {
		estimated = []string{"system", "project", "workspace_memory", "agent_memory", "tools", "history", "files", "results", "fetched", "summary"}
		count := func(text string) int {
			value, err := client.Tokenize(ctx, text, false)
			if err != nil {
				return int(math.Ceil(float64(len([]rune(text))) / 3.6))
			}
			return value
		}
		categories["system"] = count(in.SystemBase) + fallbackOverhead["system"]
		categories["project"] = max(0, count(in.SystemProject)-count(in.SystemBase))
		categories["workspace_memory"] = max(0, count(in.SystemWorkspaceMemory)-count(in.SystemProject))
		categories["agent_memory"] = max(0, count(in.System)-count(in.SystemWorkspaceMemory))
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
		if !costsCached {
			for name, schema := range in.AllSchemas {
				data, _ := json.Marshal(schema)
				schemaCounts[name] = int(math.Ceil(float64(count(string(data))) * 1.1))
			}
			fullPrefix := count(in.System) + categories["tools"]
			for name, withoutSystem := range in.WithoutToolSystems {
				withoutData, _ := json.Marshal(schemasWithout(in.Schemas, name))
				withoutTools := int(math.Ceil(float64(count(string(withoutData))) * 1.1))
				marginalCounts[name] = max(0, fullPrefix-count(withoutSystem)-withoutTools)
			}
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
		withProject := base
		if in.SystemProject != in.SystemBase {
			withProject, err = render([]llm.Message{{Role: "system", Content: in.SystemProject}}, nil)
			if err != nil {
				return events.Budget{}, err
			}
		}
		withWorkspaceMemory, err := render([]llm.Message{{Role: "system", Content: in.SystemWorkspaceMemory}}, nil)
		if err != nil {
			return events.Budget{}, err
		}
		withMemory, err := render([]llm.Message{{Role: "system", Content: in.System}}, nil)
		if err != nil {
			return events.Budget{}, err
		}
		categories["system"], categories["project"] = base, max(0, withProject-base)
		categories["workspace_memory"], categories["agent_memory"] = max(0, withWorkspaceMemory-withProject), max(0, withMemory-withWorkspaceMemory)
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
			if !costsCached {
				for name, schema := range in.AllSchemas {
					one, err := render([]llm.Message{{Role: "system", Content: in.System}}, []any{schema})
					if err != nil {
						return events.Budget{}, fmt.Errorf("count schema %s: %w", name, err)
					}
					schemaCounts[name] = max(0, one-withMemory)
				}
				for name, withoutSystem := range in.WithoutToolSystems {
					without, err := render([]llm.Message{{Role: "system", Content: withoutSystem}}, schemasWithout(in.Schemas, name))
					if err != nil {
						return events.Budget{}, fmt.Errorf("count marginal %s: %w", name, err)
					}
					marginalCounts[name] = max(0, withTools-without)
				}
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
			if !costsCached {
				for name, schema := range in.AllSchemas {
					raw, _ := json.Marshal(schema)
					value, err := client.Tokenize(ctx, string(raw), false)
					if err != nil {
						return events.Budget{}, fmt.Errorf("count schema %s: %w", name, err)
					}
					schemaCounts[name] = int(math.Ceil(float64(value) * 1.1))
				}
				for name, withoutSystem := range in.WithoutToolSystems {
					withoutBase, err := render([]llm.Message{{Role: "system", Content: withoutSystem}}, nil)
					if err != nil {
						return events.Budget{}, fmt.Errorf("count marginal system %s: %w", name, err)
					}
					withoutData, _ := json.Marshal(schemasWithout(in.Schemas, name))
					value, err := client.Tokenize(ctx, string(withoutData), false)
					if err != nil {
						return events.Budget{}, fmt.Errorf("count marginal schema %s: %w", name, err)
					}
					withoutTools := int(math.Ceil(float64(value) * 1.1))
					marginalCounts[name] = max(0, withMemory+categories["tools"]-withoutBase-withoutTools)
				}
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
				record := in.Records[index+offset]
				weightKey := messageWeightKey(profile, candidate)
				weight, cached := b.cachedMessageWeight(s.ID, record.ID, weightKey)
				if !cached {
					weight, err = client.Tokenize(ctx, messageText(candidate.Content)+candidate.ReasoningContent, false)
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
					b.saveMessageWeight(s.ID, record.ID, weightKey, weight)
				}
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
	if !costsCached {
		b.saveCosts(s.ID, cacheKey, schemaCounts, marginalCounts)
	}
	s.SetMessageCounts(messageCounts)
	s.SetToolTokens(schemaCounts, marginalCounts)
	used := 0
	for _, value := range categories {
		used += value
	}
	nctx := profile.Context.NCtx
	budget := events.Budget{NCtx: nctx, Reserve: profile.Context.ReserveOutput, Ceiling: max(0, nctx-profile.Context.ReserveOutput), UsedEst: used, Mode: mode, Estimated: len(estimated) > 0, EstimatedCategories: estimated, Categories: categories, ToolSchemaTokens: cloneCounts(schemaCounts), ToolMarginalTokens: cloneCounts(marginalCounts)}
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
func toolCostKey(profile *config.Profile, global config.GlobalContext, cpt float64, in budgetInput) string {
	value := struct {
		Profile            *config.Profile
		Accounting         string
		CharactersPerToken float64
		System             string
		Schemas            []any
		AllSchemas         map[string]any
		WithoutToolSystems map[string]string
	}{profile, global.Accounting, cpt, in.System, in.Schemas, in.AllSchemas, in.WithoutToolSystems}
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}
func messageWeightKey(profile *config.Profile, message llm.Message) string {
	value := struct {
		BaseURL string
		Model   string
		Message llm.Message
	}{profile.BaseURL, profile.Model, message}
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}
func schemasWithout(schemas []any, excluded string) []any {
	out := make([]any, 0, max(0, len(schemas)-1))
	for _, schema := range schemas {
		wrapper, _ := schema.(map[string]any)
		function, _ := wrapper["function"].(map[string]any)
		name, _ := function["name"].(string)
		if name != excluded {
			out = append(out, schema)
		}
	}
	return out
}
func cloneCounts(values map[string]int) map[string]int {
	out := make(map[string]int, len(values))
	for name, value := range values {
		out[name] = value
	}
	return out
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
	if parts, ok := value.([]any); ok {
		var text strings.Builder
		for _, part := range parts {
			object, _ := part.(map[string]any)
			if object["type"] == "text" {
				text.WriteString(fmt.Sprint(object["text"]))
			}
		}
		return text.String()
	}
	return fmt.Sprint(value)
}
