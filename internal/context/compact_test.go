package contextmgr

import (
	"fmt"
	"testing"

	"harness/internal/events"
	"harness/internal/session"
)

func TestReadFileRangesUseByteUnitsAndConfiguredDefault(t *testing.T) {
	const defaultLimit = 16 << 10
	older := `{"path":"source.go","offset":10000}`
	current := `{"path":"source.go","offset":20000,"limit":1}`
	if !supersedes("read_file", older, current, defaultLimit) {
		t.Fatal("configured default byte window should overlap the current byte")
	}
	if got := keyArgs("read_file", `{"path":"source.go"}`, defaultLimit); got != "source.go bytes 1–16384" {
		t.Fatalf("keyArgs=%q", got)
	}
}

func TestSummarizeRejectsContextGrowth(t *testing.T) {
	bus := events.NewBus()
	item := &session.Session{ID: "main"}
	for index := 0; index < 9; index++ {
		item.Append(events.Message{ID: fmt.Sprintf("m%d", index), Tokens: 10})
	}
	before := item.MessagesCopy()
	if New(bus).Summarize(item, "run", events.Message{ID: "summary", Tokens: 100}, events.CompactionSummaryData{}) {
		t.Fatal("growing summary was accepted")
	}
	after := item.MessagesCopy()
	if len(after) != len(before) || item.Snapshot(nil).CompactionCount != 0 {
		t.Fatalf("rejected summary mutated session: messages=%d compactions=%d", len(after), item.Snapshot(nil).CompactionCount)
	}
}

func TestSummarizeRecordsReduction(t *testing.T) {
	item := &session.Session{ID: "main"}
	for index := 0; index < 10; index++ {
		item.Append(events.Message{ID: fmt.Sprintf("m%d", index), Tokens: 10})
	}
	if !New(events.NewBus()).Summarize(item, "run", events.Message{ID: "summary", Tokens: 1}, events.CompactionSummaryData{}) {
		t.Fatal("reducing summary was rejected")
	}
	snapshot := item.Snapshot(nil)
	if snapshot.CompactionCount != 1 || snapshot.CompactionTokenDelta >= 0 {
		t.Fatalf("compaction aggregate=%+v", snapshot)
	}
}
