package agent

import "testing"

func TestToolErrorGuardUsesOutcomeInsteadOfTextConvention(t *testing.T) {
	guard := newRunGuards(0, 2)
	if reason, _, _ := guard.Observe("1", "tool", nil, "ordinary failure text", false); reason != "" {
		t.Fatalf("first failure stopped run: %q", reason)
	}
	if reason, _, _ := guard.Observe("2", "tool", nil, "another ordinary failure", false); reason != "tool_errors" {
		t.Fatalf("second failure reason=%q", reason)
	}
	if reason, _, _ := guard.Observe("3", "tool", nil, "error: successful content", true); reason != "" {
		t.Fatalf("successful content with error prefix counted as failure: %q", reason)
	}
}
