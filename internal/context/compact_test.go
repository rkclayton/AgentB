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

func TestReadFileLineRangesSupersedeOnlyOverlappingLineMode(t *testing.T) {
	if !supersedes("read_file", `{"path":"a","line":10,"lines":20}`, `{"path":"a","line":25,"lines":5}`, 100) {
		t.Fatal("overlapping line reads did not supersede")
	}
	if supersedes("read_file", `{"path":"a","line":10,"lines":5}`, `{"path":"a","line":25,"lines":5}`, 100) {
		t.Fatal("disjoint line reads superseded")
	}
	if supersedes("read_file", `{"path":"a","line":10,"lines":5}`, `{"path":"a","offset":10,"limit":5}`, 100) {
		t.Fatal("line and byte modes superseded each other")
	}
}

func TestRepeatedSuccessfulReadElidesImmediatelyWithExistingStubAndEvent(t *testing.T) {
	bus := events.NewBus()
	stream, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	ok := true
	item := &session.Session{ID: "main"}
	item.Append(events.Message{ID: "call-1", Role: "assistant", ToolCalls: []events.ToolCall{{ID: "read-1", Name: "read_file", Arguments: `{"path":"source.go","offset":1,"limit":100}`}}})
	item.Append(events.Message{ID: "result-1", Role: "tool", Name: "read_file", ToolCallID: "read-1", Turn: 1, Tokens: 123, OK: &ok, Content: "first body"})
	item.Append(events.Message{ID: "call-2", Role: "assistant", ToolCalls: []events.ToolCall{{ID: "read-2", Name: "read_file", Arguments: `{"path":"source.go","offset":1,"limit":100}`}}})
	item.Append(events.Message{ID: "result-2", Role: "tool", Name: "read_file", ToolCallID: "read-2", Turn: 2, Tokens: 20, OK: &ok, Content: "second body"})
	if !New(bus).Supersede(item, "run", 2, 16384, func(text string) (int, bool) { return len(text), false }) {
		t.Fatal("repeat did not elide")
	}
	messages := item.MessagesCopy()
	if got := messages[1].Content; got != "[elided: read_file source.go bytes 1–100, 123 tokens]" || !messages[1].Elided {
		t.Fatalf("stub=%q elided=%t", got, messages[1].Elided)
	}
	if item.Snapshot().CompactionCount != 1 {
		t.Fatalf("compactions=%d", item.Snapshot().CompactionCount)
	}
	seenCompaction := false
	for index := 0; index < 2; index++ {
		event := <-stream
		if event.Type == events.Compaction {
			seenCompaction = true
		}
	}
	if !seenCompaction {
		t.Fatal("repeat elision event missing")
	}
}

func TestSettleElidesOnlyChosenDesignTurns(t *testing.T) {
	item := &session.Session{ID: "plan", Messages: []events.Message{{ID: "u1", Role: "user", Content: "ramble", Tokens: 20}, {ID: "a1", Role: "assistant", Content: "answer", Tokens: 20}, {ID: "u2", Role: "user", Content: "keep", Tokens: 10}}}
	if !New(events.NewBus()).Settle(item, "run", "[settled → plan item 2t]", []string{"u1", "a1", "missing"}, func(text string) (int, bool) { return 7, false }) {
		t.Fatal("settle reported no change")
	}
	messages := item.MessagesCopy()
	if messages[0].Content != "[settled → plan item 2t]" || !messages[0].Elided || messages[1].Content != "[settled → plan item 2t]" || messages[2].Content != "keep" {
		t.Fatalf("messages=%+v", messages)
	}
}

func TestRepeatedReadNeverElidesFailedOrUnknownOutcome(t *testing.T) {
	for _, test := range []struct {
		name           string
		older, current *bool
	}{
		{"older failed", boolValue(false), boolValue(true)},
		{"current failed", boolValue(true), boolValue(false)},
		{"legacy unknown", nil, boolValue(true)},
	} {
		t.Run(test.name, func(t *testing.T) {
			item := &session.Session{ID: "main"}
			item.Append(events.Message{Role: "assistant", ToolCalls: []events.ToolCall{{ID: "one", Name: "read_file", Arguments: `{"path":"a"}`}}})
			item.Append(events.Message{ID: "older", Role: "tool", Name: "read_file", ToolCallID: "one", Turn: 1, OK: test.older, Content: "older"})
			item.Append(events.Message{Role: "assistant", ToolCalls: []events.ToolCall{{ID: "two", Name: "read_file", Arguments: `{"path":"a"}`}}})
			item.Append(events.Message{ID: "current", Role: "tool", Name: "read_file", ToolCallID: "two", Turn: 2, OK: test.current, Content: "current"})
			if New(events.NewBus()).Supersede(item, "run", 2, 16384, func(text string) (int, bool) { return len(text), false }) {
				t.Fatal("failed or unknown outcome was elided")
			}
		})
	}
}

func TestOldResultElisionNeverRemovesFailedCallRecord(t *testing.T) {
	failed, passed := false, true
	item := &session.Session{ID: "main"}
	item.Append(events.Message{ID: "failed", Role: "tool", Name: "read_file", Category: "files", Tokens: 100, OK: &failed, Content: "error: denied"})
	for index := 0; index < 5; index++ {
		item.Append(events.Message{ID: fmt.Sprintf("passed-%d", index), Role: "tool", Name: "read_file", Category: "files", Tokens: 100, OK: &passed, Content: "body"})
	}
	New(events.NewBus()).ElideOld(item, "run", 600, 0, 16384, func(text string) (int, bool) { return len(text), false })
	if got := item.MessagesCopy()[0]; got.Elided || got.Content != "error: denied" {
		t.Fatalf("failed record changed: %+v", got)
	}
}

func TestOldResultElisionRecognizesLegacyFailurePrefixWithoutOKField(t *testing.T) {
	passed := true
	item := &session.Session{ID: "main"}
	item.Append(events.Message{ID: "legacy-failed", Role: "tool", Name: "read_file", Category: "files", Tokens: 100, Content: "error: access denied"})
	for index := 0; index < 5; index++ {
		item.Append(events.Message{ID: fmt.Sprintf("passed-%d", index), Role: "tool", Name: "read_file", Category: "files", Tokens: 100, OK: &passed, Content: "body"})
	}
	New(events.NewBus()).ElideOld(item, "run", 600, 0, 16384, func(text string) (int, bool) { return len(text), false })
	if got := item.MessagesCopy()[0]; got.Elided || got.Content != "error: access denied" {
		t.Fatalf("legacy failed record changed: %+v", got)
	}
}

func boolValue(value bool) *bool { return &value }

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
	if len(after) != len(before) || item.Snapshot().CompactionCount != 0 {
		t.Fatalf("rejected summary mutated session: messages=%d compactions=%d", len(after), item.Snapshot().CompactionCount)
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
	snapshot := item.Snapshot()
	if snapshot.CompactionCount != 1 || snapshot.CompactionTokenDelta >= 0 {
		t.Fatalf("compaction aggregate=%+v", snapshot)
	}
}
