package projection

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"harness/internal/events"
)

func TestNextIsPureAndEmitsVersionedCursorPatch(t *testing.T) {
	previous := seeded(t)
	before, _ := json.Marshal(previous)
	record := Record{
		Cursor: Cursor{Generation: "main-a.jsonl", Offset: 42},
		Event: events.Event{SessionID: "main", Type: events.MessageAppended, Data: map[string]any{
			"message": events.Message{ID: "m1", Role: "user", Content: "hello", Category: "history"},
		}},
	}
	next, patch, err := Next(previous, record)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, _ := json.Marshal(previous)
	if !reflect.DeepEqual(unchanged, before) {
		t.Fatal("Next mutated its input snapshot")
	}
	if len(next.Messages) != 1 || next.Messages[0].ID != "m1" {
		t.Fatalf("messages = %#v", next.Messages)
	}
	if patch.SchemaVersion != SchemaVersion || patch.Cursor != record.Cursor || patch.SessionID != "main" {
		t.Fatalf("patch envelope = %#v", patch)
	}
	if len(patch.Operations) == 0 {
		t.Fatal("message append emitted no projection operation")
	}
}

func TestMessageAttachmentProjectsIntoChatEntry(t *testing.T) {
	state := seeded(t)
	attachment := events.Attachment{Path: "attachments/spec.txt", Bytes: 12, SHA256: strings.Repeat("a", 64)}
	next, _, err := Next(state, Record{Cursor: Cursor{Generation: "attachment.events", Offset: 200}, Event: events.Event{
		SessionID: "main", Type: events.MessageAppended,
		Data: map[string]any{"message": events.Message{ID: "m-attachment", Role: "user", Content: "review", Category: "history", Attachments: []events.Attachment{attachment}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Chat) != 1 || len(next.Chat[0].Attachments) != 1 || next.Chat[0].Attachments[0] != attachment {
		t.Fatalf("chat=%+v", next.Chat)
	}
}

func TestResetOnlyGenerationIsExplicitlyIncomplete(t *testing.T) {
	state := Empty("main")
	next, _, err := Next(state, Record{
		Cursor: Cursor{Generation: "main-reset.jsonl", Offset: 101},
		Event:  events.Event{SessionID: "main", Type: events.SessionReset, Data: map[string]any{"log_path": "next.jsonl"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.Complete {
		t.Fatal("reset-only generation was presented as a complete snapshot")
	}
	if next.Cursor.Offset != 101 || next.LogPath != "next.jsonl" {
		t.Fatalf("snapshot = %#v", next)
	}
}

func TestStreamingProjectionUsesIncrementalTextOperations(t *testing.T) {
	state := seeded(t)
	state, _, _ = Next(state, Record{Cursor: Cursor{Generation: "main-a.jsonl", Offset: 30}, Event: events.Event{TS: "2026-09-05T00:00:00Z", SessionID: "main", RunID: "r1", Type: events.ModelRequest, Data: map[string]any{"turn": 1}}})
	state, _, _ = Next(state, Record{Cursor: Cursor{Generation: "main-a.jsonl", Offset: 40}, Event: events.Event{TS: "2026-09-05T00:00:01Z", SessionID: "main", RunID: "r1", Type: events.ModelDelta, Data: map[string]any{"turn": 1, "kind": "reasoning", "text": "first"}}})
	_, patch, err := Next(state, Record{Cursor: Cursor{Generation: "main-a.jsonl", Offset: 50}, Event: events.Event{TS: "2026-09-05T00:00:02Z", SessionID: "main", RunID: "r1", Type: events.ModelDelta, Data: map[string]any{"turn": 1, "kind": "reasoning", "text": " second"}}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, operation := range patch.Operations {
		if operation.Op == "append" && strings.HasSuffix(operation.Path, "/reasoning") && string(operation.Value) == `" second"` {
			found = true
		}
	}
	if !found {
		t.Fatalf("operations=%+v", patch.Operations)
	}
}

func TestBudgetReplacementDoesNotRetainOmittedFields(t *testing.T) {
	state := seeded(t)
	state.Budget.ToolMarginalTokens = map[string]int{"removed_tool": 145}
	next, _, err := Next(state, Record{
		Cursor: Cursor{Generation: "main-a.jsonl", Offset: 80},
		Event: events.Event{SessionID: "main", Type: events.BudgetEvent, Data: map[string]any{
			"n_ctx": 32768, "categories": map[string]int{"tools": 1221},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.Budget.ToolMarginalTokens != nil {
		t.Fatalf("omitted marginal map survived replacement: %#v", next.Budget.ToolMarginalTokens)
	}
}

func TestMessageRemovalDeletesProjectedMessageAndChatEntry(t *testing.T) {
	state := seeded(t)
	appended := Record{Cursor: Cursor{Generation: "main-a.jsonl", Offset: 30}, Event: events.Event{
		SessionID: "main", Type: events.MessageAppended,
		Data: map[string]any{"message": events.Message{ID: "m1", Role: "user", Content: "remove me", Category: "history"}},
	}}
	state, _, _ = Next(state, appended)
	removed := Record{Cursor: Cursor{Generation: "main-a.jsonl", Offset: 50}, Event: events.Event{
		SessionID: "main", Type: events.MessageRemoved, Data: map[string]any{"id": "m1", "reason": "operator_repair"},
	}}
	next, patch, err := Next(state, removed)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Messages) != 0 || len(next.Chat) != 0 {
		t.Fatalf("messages=%#v chat=%#v", next.Messages, next.Chat)
	}
	if len(patch.Operations) == 0 {
		t.Fatal("removal emitted no patch")
	}
}

func TestFilesDeliveredProjectsAsDurableChatNotice(t *testing.T) {
	state := seeded(t)
	record := Record{Cursor: Cursor{Generation: "main-a.jsonl", Offset: 30}, Event: events.Event{
		Seq: 9, SessionID: "main", RunID: "r1", Type: events.FilesDelivered,
		Data: map[string]any{"mode": "both", "items": []any{map[string]any{"source_path": "done.txt", "delivered_path": `C:\\Users\\operator\\Agent_b\\done.txt`, "status": "copied"}}},
	}}
	next, _, err := Next(state, record)
	if err != nil {
		t.Fatal(err)
	}
	entry := next.Chat[len(next.Chat)-1]
	if entry.Type != "notice" || entry.RunID != "r1" || entry.Event == nil || entry.Event.Type != events.FilesDelivered {
		t.Fatalf("chat entry=%+v", entry)
	}
}

func TestShellGrantAndLapseProjectAsReplayableNotices(t *testing.T) {
	state := seeded(t)
	grant := Record{Cursor: Cursor{Generation: "main-a.jsonl", Offset: 30}, Event: events.Event{
		Seq: 9, SessionID: "main", RunID: "r1", Type: events.ShellGrant,
		Data: map[string]any{"run_id": "r1", "scope": "run", "rule": "operator_command", "identity": "operator", "executable": `C:\Program Files\Git\cmd\git.exe`},
	}}
	next, _, err := Next(state, grant)
	if err != nil {
		t.Fatal(err)
	}
	lapse := Record{Cursor: Cursor{Generation: "main-a.jsonl", Offset: 60}, Event: events.Event{
		Seq: 10, SessionID: "main", RunID: "r1", Type: events.ShellGrantLapsed,
		Data: map[string]any{"run_id": "r1", "scope": "run", "rule": "operator_command", "identity": "operator", "executable": `C:\Program Files\Git\cmd\git.exe`, "reason": "run ended"},
	}}
	next, _, err = Next(next, lapse)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Chat) < 2 || next.Chat[len(next.Chat)-2].Event.Type != events.ShellGrant || next.Chat[len(next.Chat)-1].Event.Type != events.ShellGrantLapsed {
		t.Fatalf("chat=%+v", next.Chat)
	}
}

func TestModelAvailabilityAndRunAsYouReconstructFromEvents(t *testing.T) {
	state := Empty("main")
	records := []events.Event{
		events.New(events.ModelUnreachable, "main", "r1", map[string]any{"host": "model.example:8000", "detail": "dial timeout"}),
		events.New(events.ShellGrant, "main", "r1", map[string]any{"scope": "session", "identity": "operator", "rule": "shell_boundary"}),
		events.New(events.ModelReachable, "main", "", map[string]any{"server_id": "main"}),
		events.New(events.ShellGrantLapsed, "main", "r1", map[string]any{"scope": "session", "identity": "operator", "reason": "revoked by operator"}),
	}
	for index, event := range records {
		var err error
		state, _, err = Next(state, Record{Cursor: Cursor{Generation: "availability.events", Offset: int64(index + 1)}, Event: event})
		if err != nil {
			t.Fatal(err)
		}
	}
	if state.ModelUnreachable != nil || state.RunAsYou {
		t.Fatalf("replayed availability=%+v run_as_you=%t", state.ModelUnreachable, state.RunAsYou)
	}
}

func TestSessionRenameReplaysAuthorAndUserPin(t *testing.T) {
	state := seeded(t)
	aux, _, err := Next(state, Record{Cursor: Cursor{Generation: "main-a.jsonl", Offset: 30}, Event: events.Event{
		Seq: 9, SessionID: "main", Type: events.SessionRenamed, Data: map[string]any{"label": "Aux title", "by": "aux"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if aux.Label != "Aux title" || aux.NamePinned {
		t.Fatalf("aux rename=%+v", aux)
	}
	user, _, err := Next(aux, Record{Cursor: Cursor{Generation: "main-a.jsonl", Offset: 60}, Event: events.Event{
		Seq: 10, SessionID: "main", Type: events.SessionRenamed, Data: map[string]any{"label": "Pinned title", "by": "user"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if user.Label != "Pinned title" || !user.NamePinned {
		t.Fatalf("user rename=%+v", user)
	}
}

func TestRunStoppingAndHeldQueueReplay(t *testing.T) {
	state := Empty("main")
	state.Run = Run{Status: "running", RunID: "r1", MaxTurns: 40}
	stopping, _, err := Next(state, Record{Cursor: Cursor{Generation: "main.events", Offset: 10}, Event: events.Event{Seq: 1, SessionID: "main", RunID: "r1", Type: events.RunStopping, Data: map[string]any{"reason": "safe"}}})
	if err != nil || stopping.Run.Status != "stopping" {
		t.Fatalf("stopping=%+v err=%v", stopping.Run, err)
	}
	stopping.QueuedMessages = 2
	held, _, err := Next(stopping, Record{Cursor: Cursor{Generation: "main.events", Offset: 20}, Event: events.Event{Seq: 2, SessionID: "main", RunID: "r1", Type: events.RunStopped, Data: map[string]any{"reason": "safe", "queue_held": true}}})
	if err != nil || held.Run.Status != "held" || held.Run.LastStopReason != "safe" || held.Run.QueuePosition != 2 {
		t.Fatalf("held=%+v err=%v", held.Run, err)
	}
}

func TestReadFileUsesDurableByteBoundary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main-a.jsonl")
	one := events.Event{Seq: 1, TS: "2026-09-05T00:00:00Z", SessionID: "main", Type: events.SessionReset, Data: map[string]any{}}
	two := events.Event{Seq: 2, TS: "2026-09-05T00:00:01Z", SessionID: "main", Type: events.SessionRenamed, Data: map[string]any{"label": "next"}}
	first, _ := json.Marshal(one)
	second, _ := json.Marshal(two)
	content := append(append(append([]byte{}, first...), '\n'), second...)
	content = append(content, '\n')
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	boundary := int64(len(first) + 1)
	records, offset, err := ReadFile(path, boundary)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || offset != boundary || records[0].Cursor.Offset != boundary {
		t.Fatalf("records=%d offset=%d cursor=%#v", len(records), offset, records[0].Cursor)
	}
	if _, _, err := ReadFile(path, boundary-1); err == nil {
		t.Fatal("non-record byte offset was accepted")
	}
}

func TestCacheIsKeyedByDurableOffsetAndDiscardable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main-a.jsonl")
	created := events.Event{Seq: 1, TS: "2026-09-05T00:00:00Z", SessionID: "main", Type: events.SessionCreated, Data: map[string]any{
		"session": map[string]any{"id": "main", "label": "main", "run": map[string]any{"status": "idle"}, "tools": []any{}, "messages": []any{}},
	}}
	encoded, _ := json.Marshal(created)
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	cache := NewCache()
	first, err := cache.ProjectFile(path, int64(len(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	first.Label = "mutated caller copy"
	cached, err := cache.ProjectFile(path, int64(len(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	if cached.Label != "main" {
		t.Fatalf("caller mutation changed cached snapshot: %#v", cached)
	}
	cache.Clear()
	second, err := cache.ProjectFile(path, int64(len(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cached, second) || !second.Complete {
		t.Fatalf("rebuilt snapshot differs: cached=%#v second=%#v", cached, second)
	}
}

func seeded(t *testing.T) Snapshot {
	t.Helper()
	next, _, err := Next(Empty("main"), Record{
		Cursor: Cursor{Generation: "main-a.jsonl", Offset: 20},
		Event: events.Event{SessionID: "main", Type: events.SessionCreated, Data: map[string]any{
			"session": map[string]any{
				"id": "main", "label": "main", "server_id": "homepc", "workspace": "workspace",
				"run":   map[string]any{"status": "idle", "max_turns": 40},
				"tools": []any{}, "messages": []any{}, "runnable": true,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func cloneSnapshot(t *testing.T, value Snapshot) Snapshot {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result Snapshot
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
