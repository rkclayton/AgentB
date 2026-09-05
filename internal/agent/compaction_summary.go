package agent

import (
	"context"
	"fmt"
	"math"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/llm"
	"harness/internal/session"
)

const compactionMaxTokens = 800

func (r *Runner) summarize(ctx context.Context, s *session.Session, runID string, main *config.Profile) bool {
	records := s.MessagesCopy()
	if len(records) <= 7 {
		return false
	}
	cfg := r.cfg()
	if cfg.Roles.Aux == "" {
		accepted, _ := r.trySummary(ctx, s, runID, main, main, "main", "", 0, false)
		return accepted
	}
	aux, ok := cfg.Profile(cfg.Roles.Aux)
	if !ok {
		accepted, _ := r.trySummary(ctx, s, runID, main, main, "main", "aux_profile", 0, false)
		return accepted
	}
	if aux.ID == main.ID {
		accepted, _ := r.trySummary(ctx, s, runID, main, aux, "aux", "", 0, false)
		return accepted
	}

	auxRequestProfile := summaryProfile(aux)
	auxMessages := r.summaryMessages(&auxRequestProfile, s)
	promptTokens, estimated, err := compactionPromptTokens(ctx, &auxRequestProfile, auxMessages, cfg.Context.Accounting)
	if err != nil {
		r.publishSummaryAttempt(s, runID, events.CompactionSummaryData{Role: "aux", ProfileID: aux.ID, Model: aux.Model, Outcome: "error", Reason: "fit check: " + err.Error(), NCtx: aux.Context.NCtx})
		accepted, _ := r.trySummary(ctx, s, runID, main, main, "main", "aux_fit_error", 0, false)
		return accepted
	}
	guard := promptTokens
	if estimated {
		guard = int(math.Ceil(float64(guard) * 1.10))
	}
	if aux.Context.NCtx <= 0 || guard+compactionMaxTokens > aux.Context.NCtx {
		reason := fmt.Sprintf("prompt %d%s + reserve %d exceeds n_ctx %d", promptTokens, estimatedLabel(estimated), compactionMaxTokens, aux.Context.NCtx)
		r.publishSummaryAttempt(s, runID, events.CompactionSummaryData{Role: "aux", ProfileID: aux.ID, Model: aux.Model, Outcome: "skipped", Reason: reason, EstimatedPromptTokens: promptTokens, Estimated: estimated, NCtx: aux.Context.NCtx})
		accepted, _ := r.trySummary(ctx, s, runID, main, main, "main", "aux_context", 0, false)
		return accepted
	}

	accepted, failure := r.trySummary(ctx, s, runID, main, aux, "aux", "", promptTokens, estimated)
	if accepted {
		return true
	}
	fallback := "aux_" + failure
	accepted, _ = r.trySummary(ctx, s, runID, main, main, "main", fallback, 0, false)
	return accepted
}

func (r *Runner) trySummary(ctx context.Context, s *session.Session, runID string, sessionProfile, servingProfile *config.Profile, role, fallback string, estimatedPromptTokens int, estimated bool) (bool, string) {
	profile := summaryProfile(servingProfile)
	messages := r.summaryMessages(&profile, s)
	started := time.Now()
	response, err := llm.New(&profile).Chat(ctx, llm.Request{Messages: messages, MaxTokens: compactionMaxTokens, Thinking: profile.Reasoning.Enabled})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		s.RecordCompactionModel(0, 0)
		r.publishSummaryAttempt(s, runID, events.CompactionSummaryData{Role: role, ProfileID: profile.ID, Model: profile.Model, Outcome: "error", Reason: err.Error(), FallbackReason: fallback, Dispatched: true, EstimatedPromptTokens: estimatedPromptTokens, Estimated: estimated, NCtx: profile.Context.NCtx, DurationMS: duration})
		if role == "main" {
			r.operationalError(s, runID, "compaction_summary", err)
		}
		return false, "error"
	}
	if response.DurationMS <= 0 {
		response.DurationMS = duration
	}
	cached := nullableInt(response.Usage.CachedTokens)
	source := events.CompactionSummaryData{Role: role, ProfileID: profile.ID, Model: profile.Model, FallbackReason: fallback, Dispatched: true, EstimatedPromptTokens: estimatedPromptTokens, Estimated: estimated, NCtx: profile.Context.NCtx, Usage: events.ModelUsage{PromptTokens: response.Usage.PromptTokens, CompletionTokens: response.Usage.CompletionTokens, CachedTokens: cached}, DurationMS: response.DurationMS}
	s.RecordCompactionModel(response.Usage.PromptTokens, response.Usage.CompletionTokens)
	message, _ := r.makeMessage(ctx, sessionProfile, "user", "Progress note (auto-summary of earlier turns):\n"+response.Content, "summary", 0)
	if !r.compact.Summarize(s, runID, message, source) {
		return false, "rejected"
	}
	r.bus.Publish(events.New(events.MessageAppended, s.ID, runID, map[string]any{"message": message}))
	return true, ""
}

func (r *Runner) summaryMessages(profile *config.Profile, s *session.Session) []llm.Message {
	messages := []llm.Message{{Role: "system", Content: r.prompt.Render(profile, s, r.tools.Names(s.EnabledTools()), s.MemoryBlock)}}
	for _, message := range s.MessagesCopy() {
		if message.Elided || (message.Category != "history" && message.Category != "summary") {
			continue
		}
		messages = append(messages, llm.Message{Role: message.Role, Content: message.Content})
	}
	return append(messages, llm.Message{Role: "user", Content: "Summarize the work so far for your own future reference: the task, files touched and what changed in each, decisions made, and what remains. Under 300 words. No preamble."})
}

func summaryProfile(profile *config.Profile) config.Profile {
	result := *profile
	result.Sampling.Thinking.Temperature = .3
	result.Sampling.Nonthinking.Temperature = .3
	if len(result.Reasoning.ValidEfforts) > 0 {
		result.Reasoning.Effort = result.Reasoning.ValidEfforts[0]
	}
	return result
}

func compactionPromptTokens(ctx context.Context, profile *config.Profile, messages []llm.Message, accounting string) (int, bool, error) {
	client := llm.New(profile)
	if accounting != "estimated" && profile.Capabilities.Tokenize {
		if profile.Capabilities.ApplyTemplate {
			prompt, err := client.ApplyTemplate(ctx, messages, nil)
			if err != nil {
				return 0, false, err
			}
			tokens, err := client.Tokenize(ctx, prompt, false)
			return tokens, false, err
		}
		total := 0
		for _, message := range messages {
			tokens, err := client.Tokenize(ctx, messageText(message.Content)+message.ReasoningContent, false)
			if err != nil {
				return 0, true, err
			}
			total += tokens + fallbackOverhead[message.Role]
		}
		return total, true, nil
	}
	total := 0
	for _, message := range messages {
		total += int(math.Ceil(float64(len([]rune(messageText(message.Content)+message.ReasoningContent))) / 3.6))
		total += fallbackOverhead[message.Role]
	}
	return total, true, nil
}

func (r *Runner) publishSummaryAttempt(s *session.Session, runID string, data events.CompactionSummaryData) {
	r.bus.Publish(events.New(events.CompactionSummary, s.ID, runID, data))
}

func nullableInt(value int) *int {
	if value < 0 {
		return nil
	}
	return &value
}

func estimatedLabel(estimated bool) string {
	if estimated {
		return " estimated (10% guard)"
	}
	return ""
}
