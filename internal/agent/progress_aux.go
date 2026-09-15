package agent

import (
	"context"
	"strings"

	"harness/internal/events"
	"harness/internal/llm"
	"harness/internal/session"
)

func (r *Runner) maybeAuxProgress(ctx context.Context, item *session.Session, runID string, turn int) {
	if turn == 0 || turn%10 != 0 {
		return
	}
	cfg := r.cfg()
	agentConfig, ok := cfg.Agent(item.Snapshot().AgentID)
	if !ok || agentConfig.C == "" {
		return
	}
	profile, ok := cfg.Profile(agentConfig.C)
	if !ok {
		return
	}
	transcript := progressTranscript(item.MessagesCopy())
	if transcript == "" {
		return
	}
	response, err := llm.New(profile).Chat(ctx, llm.Request{Messages: []llm.Message{
		{Role: "system", Content: "Classify whether this tool trajectory is making progress toward its stated goal. Reply with exactly productive, stuck, or mixed."},
		{Role: "user", Content: transcript},
	}, MaxTokens: 8, Thinking: false})
	data := map[string]any{"turn": turn, "available": err == nil}
	if err == nil {
		verdict := strings.ToLower(strings.TrimSpace(response.Content))
		if verdict != "productive" && verdict != "stuck" && verdict != "mixed" {
			verdict = "mixed"
		}
		data["verdict"] = verdict
	}
	r.bus.Publish(events.New(events.ProgressAux, item.ID, runID, data))
}

func progressTranscript(messages []events.Message) string {
	const limit = 6000
	var lines []string
	for _, message := range messages {
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			for _, call := range message.ToolCalls {
				lines = append(lines, "call "+call.Name+" "+call.Arguments)
			}
		}
		if message.Role == "tool" {
			lines = append(lines, "result "+message.Name+" "+message.Content)
		}
	}
	value := strings.Join(lines, "\n")
	if len(value) > limit {
		value = value[len(value)-limit:]
	}
	return value
}
