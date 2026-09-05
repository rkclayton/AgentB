package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReplayPreservesHistoricalToolNamesWithoutLiveRegistry(t *testing.T) {
	session := ReplaySession{
		ID: "old", Label: "historical", Run: ReplayRun{Status: "idle"}, Runnable: true,
		Tools: []ReplayTool{{Name: "grep", Enabled: true}, {Name: "glob", Enabled: true}, {Name: "fetch", Enabled: true}},
	}
	events := []Event{
		{TS: "2026-01-01T00:00:00Z", SessionID: "old", Type: SessionCreated, Data: map[string]any{"session": session}},
		{TS: "2026-01-01T00:00:01Z", SessionID: "old", Type: ToolResult, Data: map[string]any{"name": "grep"}},
		{TS: "2026-01-01T00:00:02Z", SessionID: "old", Type: ToolToggled, Data: map[string]any{"name": "glob", "enabled": false}},
		{TS: "2026-01-01T00:00:03Z", SessionID: "old", Type: ApprovalRequired, Data: map[string]any{"name": "fetch"}},
		{TS: "2026-01-01T00:00:04Z", SessionID: "old", Type: MessageAppended, Data: map[string]any{"message": Message{ID: "tool", Role: "tool", Name: "fetch", Content: "historical", Category: "fetched"}}},
	}
	path := filepath.Join(t.TempDir(), "historical.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if err := json.NewEncoder(file).Encode(event); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	replay, err := LoadReplay([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	item := replay.Sessions["old"]
	if len(item.Tools) != 3 || item.Tools[0].Name != "grep" || item.Tools[0].Calls != 1 || item.Tools[1].Name != "glob" || item.Tools[1].Enabled || item.Tools[2].Name != "fetch" {
		t.Fatalf("historical tools changed during replay: %#v", item.Tools)
	}
	if len(item.Messages) != 1 || item.Messages[0].Name != "fetch" {
		t.Fatalf("historical tool message changed during replay: %#v", item.Messages)
	}
	if got := replayString(replay.Events[3].Data.(map[string]any)["name"]); got != "fetch" {
		t.Fatalf("historical approval name = %q, want fetch", got)
	}
}

func TestReplayResetClearsToolCounts(t *testing.T) {
	sessions := map[string]ReplaySession{
		"main": {ID: "main", Tools: []ReplayTool{{Name: "read_file", Calls: 3}}},
	}
	ReduceReplay(sessions, Event{SessionID: "main", Type: SessionReset})
	if got := sessions["main"].Tools[0].Calls; got != 0 {
		t.Fatalf("calls after reset=%d, want 0", got)
	}
}
