package logic

import (
	"reflect"
	"testing"
)

func TestDefaultMaxTurns(t *testing.T) {
	if got := DefaultMaxTurns(); got != 10000 { t.Fatalf("default max turns=%d, want 10000", got) }
}

func TestBPlanWriteRefusal(t *testing.T) {
	if CanWritePlan("b") || !CanWritePlan("d") { t.Fatalf("plan write policy is inverted") }
}

func TestTouchedPlanRoots(t *testing.T) {
	want := []string{"scratch", "repo-a", "repo-b"}
	if got := PlanRoots("scratch", []string{"repo-a", "repo-b"}); !reflect.DeepEqual(got, want) { t.Fatalf("roots=%v, want %v", got, want) }
}

func TestAbortRecordRole(t *testing.T) {
	if got := AbortRecordRole(); got != "assistant" { t.Fatalf("abort role=%q, want assistant", got) }
}

func TestRetentionPreservesNestedEvidence(t *testing.T) {
	if !ShouldDeleteWorkingLog("expired.jsonl") { t.Fatal("top-level working log was retained") }
	if ShouldDeleteWorkingLog("evidence/expired.jsonl") { t.Fatal("nested evidence log was deleted") }
}
