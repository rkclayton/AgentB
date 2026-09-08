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

func TestCycleGuardRequiresConsecutivePairOrThirdMatch(t *testing.T) {
	guard := newRunGuards(8, 10)
	if reason, _, _ := guard.Observe("1", "list_dir", map[string]any{"path": "."}, "same", true); reason != "" {
		t.Fatal(reason)
	}
	if reason, _, _ := guard.Observe("2", "read_file", map[string]any{"path": "a"}, "other", true); reason != "" {
		t.Fatal(reason)
	}
	if reason, _, _ := guard.Observe("3", "list_dir", map[string]any{"path": "."}, "same", true); reason != "" {
		t.Fatalf("non-consecutive second match=%q", reason)
	}
	if reason, _, _ := guard.Observe("4", "read_file", map[string]any{"path": "b"}, "different", true); reason != "" {
		t.Fatal(reason)
	}
	if reason, _, _ := guard.Observe("5", "list_dir", map[string]any{"path": "."}, "same", true); reason != "cycle" {
		t.Fatalf("third match=%q", reason)
	}
	guard.ResetCycle()
	_, _, _ = guard.Observe("6", "read_file", map[string]any{"path": "x"}, "same", true)
	if reason, _, _ := guard.Observe("7", "read_file", map[string]any{"path": "x"}, "same", true); reason != "cycle" {
		t.Fatalf("consecutive pair=%q", reason)
	}
}
