package projection

import (
	"os"
	"path/filepath"
	"testing"

	"harness/internal/events"
)

func TestApprovalParallelTurn2PinProjectsPendingDecision(t *testing.T) {
	snapshot := projectPin(t, "approval-parallel-turn2.events")
	if snapshot.PendingApproval == nil || snapshot.PendingApproval.Event == nil {
		t.Fatal("turn-two parallel approval was not projected as pending")
	}
	data := eventMap(snapshot.PendingApproval.Event.Data)
	if data["call_id"] != "shell-2" || data["name"] != "shell.operator_command" || snapshot.Run.Status != "paused" {
		t.Fatalf("pending=%+v run=%+v", snapshot.PendingApproval, snapshot.Run)
	}
	if len(snapshot.Chat) < 7 || snapshot.Chat[len(snapshot.Chat)-1].Key != snapshot.PendingApproval.Key {
		t.Fatalf("approval missing from durable Chat projection: %+v", snapshot.Chat)
	}
}

func TestQueuedMessageCountReconstructsFromDurableEvents(t *testing.T) {
	state := Empty("session")
	for index, event := range []events.Event{
		events.New(events.MessageQueued, "session", "", map[string]any{"message_id": "m-1", "position": 1}),
		events.New(events.MessageQueued, "session", "", map[string]any{"message_id": "m-2", "position": 2}),
		events.New(events.RunStarted, "session", "r1", map[string]any{"run_id": "r1", "user_message_id": "m-1"}),
	} {
		event.Seq = int64(index + 1)
		var err error
		state, _, err = Next(state, Record{Cursor: Cursor{Generation: "queue.events", Offset: int64(index + 1)}, Event: event})
		if err != nil {
			t.Fatal(err)
		}
	}
	if state.QueuedMessages != 1 || state.Run.RunID != "r1" || state.Run.Status != "running" {
		t.Fatalf("replayed queue state=%+v queued=%d", state.Run, state.QueuedMessages)
	}
}

func TestReplayReconstructsSupersededDecisionAndNewestPendingCard(t *testing.T) {
	state := Empty("session")
	inputs := []events.Event{
		events.New(events.ApprovalRequired, "session", "r1", map[string]any{"call_id": "first", "name": "read_file.operator_override", "boundary_escape": true}),
		events.New(events.ApprovalDecided, "session", "r1", map[string]any{"call_id": "first", "decision": "superseded"}),
		events.New(events.ApprovalRequired, "session", "r1", map[string]any{"call_id": "second", "name": "shell.operator_override", "boundary_escape": true}),
	}
	for index, event := range inputs {
		event.Seq = int64(index + 1)
		var err error
		state, _, err = Next(state, Record{Cursor: Cursor{Generation: "approval.events", Offset: int64(index + 1)}, Event: event})
		if err != nil {
			t.Fatal(err)
		}
	}
	if state.PendingApproval == nil || stringValue(eventMap(state.PendingApproval.Event.Data)["call_id"]) != "second" {
		t.Fatalf("pending=%+v", state.PendingApproval)
	}
	if len(state.Chat) != 2 || state.Chat[0].Decision != "superseded" || state.Chat[1].Decision != "" {
		t.Fatalf("chat=%+v", state.Chat)
	}
}

func TestStopPreservesChatPinAndDismissesPendingDecision(t *testing.T) {
	snapshot := projectPin(t, "stop-preserves-chat.events")
	if snapshot.PendingApproval != nil || snapshot.Run.Status != "idle" || snapshot.Run.LastStopReason != "user_stop" {
		t.Fatalf("pending=%+v run=%+v", snapshot.PendingApproval, snapshot.Run)
	}
	if len(snapshot.Messages) != 5 || len(snapshot.Chat) < 7 || snapshot.Chat[0].Text != "keep this visible" {
		t.Fatalf("messages=%d chat=%+v", len(snapshot.Messages), snapshot.Chat)
	}
	dismissed := false
	for _, entry := range snapshot.Chat {
		if entry.Event != nil && entry.Event.Type == "approval.required" && entry.Decision == "dismissed" {
			dismissed = true
		}
	}
	if !dismissed {
		t.Fatalf("dismissed approval missing from retained Chat: %+v", snapshot.Chat)
	}
}

func TestParallelCancellationUpdatesChatRowsWithoutReplacingTranscript(t *testing.T) {
	before := []ChatEntry{
		{Type: "user", Key: "message:user", Text: "keep this visible"},
		{Type: "tool", Key: "tool:shell", Text: "running"},
		{Type: "tool", Key: "tool:list", Text: "running"},
		{Type: "tool", Key: "tool:read", Text: "running"},
	}
	after := append([]ChatEntry(nil), before...)
	after[1].Text, after[2].Text, after[3].Text = "canceled", "canceled", "canceled"
	operations := diffChat(before, after)
	if len(operations) != 3 {
		t.Fatalf("operations=%+v", operations)
	}
	for _, operation := range operations {
		if operation.Op != "upsert" || operation.Path == "/chat" {
			t.Fatalf("parallel cancellation replaced Chat: %+v", operations)
		}
	}
}

func projectPin(t *testing.T, name string) Snapshot {
	t.Helper()
	path := filepath.Join("testdata", "pins", "sources", name)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewCache().ProjectFile(path, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
