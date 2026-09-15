package events

import "testing"

func TestHumanNoticeForApprovalUsesSentenceOrder(t *testing.T) {
	notice := HumanNoticeFor(ApprovalRequired, map[string]any{"name": "write_file"})
	if notice.Happened != "write_file needs your approval before it can continue." ||
		notice.HarnessAction != "The harness paused before running the action." ||
		notice.Question != "Allow this action?" || len(notice.Actions) != 3 {
		t.Fatalf("notice=%+v", notice)
	}
}

func TestHumanNoticeForStoppedRunKeepsRawReasonOutOfLead(t *testing.T) {
	notice := HumanNoticeFor(RunStopped, map[string]any{"reason": "model_unreachable", "detail": "raw socket error"})
	if notice.Happened != "The run stopped because of model unreachable." || notice.Question != "What should happen next?" {
		t.Fatalf("notice=%+v", notice)
	}
}
