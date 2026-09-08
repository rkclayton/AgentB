package agent

import (
	"context"
	"strings"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
	"harness/internal/tools"
)

func firstToolName(calls []events.ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	return calls[0].Name
}

// repairMalformedToolCall removes an assistant tool-call structure that cannot be
// rendered, retains its prose, and appends the same actionable note used at ingest.
func (r *Runner) repairMalformedToolCall(ctx context.Context, s *session.Session, runID string, p *config.Profile, currentReasoning map[string]bool) bool {
	records := s.MessagesCopy()
	badIndex := -1
	var badCalls []events.ToolCall
	for index := len(records) - 1; index >= 0; index-- {
		if records[index].Role != "assistant" || len(records[index].ToolCalls) == 0 {
			continue
		}
		for _, call := range records[index].ToolCalls {
			if _, err := tools.DecodeArgs(call.Arguments); err != nil {
				badIndex, badCalls = index, append([]events.ToolCall(nil), records[index].ToolCalls...)
				break
			}
		}
		if badIndex >= 0 {
			break
		}
	}
	if badIndex < 0 {
		return false
	}

	callIDs := map[string]bool{}
	for _, call := range badCalls {
		callIDs[call.ID] = true
	}
	bad := records[badIndex]
	kept := make([]events.Message, 0, len(records)+1)
	removed := []events.Message{}
	for index, message := range records {
		if index == badIndex {
			if strings.TrimSpace(message.Content) == "" {
				removed = append(removed, message)
				continue
			}
			message.ToolCalls = nil
			kept = append(kept, message)
			continue
		}
		if message.Role == "tool" && callIDs[message.ToolCallID] {
			removed = append(removed, message)
			continue
		}
		kept = append(kept, message)
	}
	s.ReplaceMessages(kept)
	if strings.TrimSpace(bad.Content) != "" {
		r.bus.Publish(events.New(events.MessageUpdated, s.ID, runID, map[string]any{"id": bad.ID, "patch": map[string]any{"tool_calls": []events.ToolCall{}}}))
		currentReasoning[bad.ID] = true
	}
	for _, message := range removed {
		r.bus.Publish(events.New(events.MessageRemoved, s.ID, runID, map[string]any{"id": message.ID, "reason": "unrenderable_tool_call"}))
	}
	r.bus.Publish(events.New(events.ModelRetry, s.ID, runID, map[string]any{"turn": bad.Turn, "reason": "malformed_tool_history", "tool": firstToolName(badCalls), "attempt": 1, "max_attempts": 1}))
	return true
}
