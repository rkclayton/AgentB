package events

import (
	"fmt"
	"strings"
)

// HumanNotice is the shared operator-facing order for pending cards and
// external notification sinks. Raw event data stays available as collapsed
// technical detail; it is never the lead.
type HumanNotice struct {
	Happened      string   `json:"happened"`
	HarnessAction string   `json:"harness_action"`
	Question      string   `json:"question,omitempty"`
	Actions       []string `json:"actions,omitempty"`
}

func WithHuman(eventType string, data map[string]any) map[string]any {
	result := make(map[string]any, len(data)+1)
	for key, value := range data {
		result[key] = value
	}
	result["human"] = HumanNoticeFor(eventType, result)
	return result
}

func HumanNoticeFor(eventType string, data map[string]any) HumanNotice {
	switch eventType {
	case ApprovalRequired:
		name := textValue(data["name"], "This action")
		if data["kind"] == "cycle" || name == "run.cycle" {
			return HumanNotice{
				Happened:      "The same action and result repeated without progress.",
				HarnessAction: "The harness paused the run without starting another turn.",
				Question:      "Continue this run?",
				Actions:       []string{"Continue", "Stop"},
			}
		}
		return HumanNotice{
			Happened:      fmt.Sprintf("%s needs your approval before it can continue.", name),
			HarnessAction: "The harness paused before running the action.",
			Question:      "Allow this action?",
			Actions:       []string{"Yes, for this chat", "Just once", "No"},
		}
	case RunStopped:
		reason := strings.ReplaceAll(textValue(data["reason"], "an unknown reason"), "_", " ")
		if reason == "done" {
			return HumanNotice{Happened: "The run finished.", HarnessAction: "The harness kept the completed work in this chat."}
		}
		return HumanNotice{
			Happened:      fmt.Sprintf("The run stopped because of %s.", reason),
			HarnessAction: "The harness kept completed work and did not start another action.",
			Question:      "What should happen next?",
			Actions:       []string{"Continue", "Revise plan", "Stop"},
		}
	case ItemDone:
		return HumanNotice{Happened: "The current plan item finished.", HarnessAction: "The harness kept its completed work and advanced the plan record."}
	case PlanDone:
		return HumanNotice{Happened: "The plan finished.", HarnessAction: "The harness kept the completed work and returned the chat to you."}
	default:
		return HumanNotice{}
	}
}

func textValue(value any, fallback string) string {
	if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
		return strings.TrimSpace(text)
	}
	return fallback
}
