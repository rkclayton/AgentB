package session

import (
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
)

func TestSnapshotCarriesToolCallCounts(t *testing.T) {
	s := &Session{
		ToolsEnabled:   map[string]bool{"read_file": true},
		ToolCalls:      map[string]int{},
		SchemaTokens:   map[string]int{"read_file": 42},
		MarginalTokens: map[string]int{"read_file": 17},
	}
	s.IncrementToolCall("read_file")
	s.IncrementToolCall("read_file")
	s.IncrementToolCall("unknown")
	snapshot := s.Snapshot()
	if len(snapshot.Tools) != 1 || snapshot.Tools[0].Calls != 2 || snapshot.Tools[0].SchemaTokens != 42 || snapshot.Tools[0].MarginalTokens != 17 {
		t.Fatalf("snapshot tools=%+v", snapshot.Tools)
	}
}

func TestSnapshotCarriesRunAggregates(t *testing.T) {
	s := &Session{}
	s.RecordModelTurn()
	s.RecordModelTurn()
	s.RecordCompaction(-123)
	s.RecordCompactionModel(400, 50)
	snapshot := s.Snapshot()
	if snapshot.ModelTurns != 2 || snapshot.CompactionCount != 1 || snapshot.CompactionTokenDelta != -123 || snapshot.CompactionModelCalls != 1 || snapshot.CompactionPrompt != 400 || snapshot.CompactionCompletion != 50 {
		t.Fatalf("snapshot aggregates=%+v", snapshot)
	}
}

func TestCloseIsDurableMetadataAndNeverStopsARun(t *testing.T) {
	running := &Session{Run: RunState{Status: "running"}}
	if err := running.Close(); err == nil || !strings.Contains(err.Error(), "stop the run before closing") || running.IsClosed() {
		t.Fatalf("running close err=%v closed=%t", err, running.IsClosed())
	}
	idle := &Session{Run: RunState{Status: "idle"}}
	if err := idle.Close(); err != nil || !idle.Snapshot().Closed {
		t.Fatalf("idle close err=%v snapshot=%+v", err, idle.Snapshot())
	}
}

func TestCreateLikeKeepsProfileWorkspaceAndExactToolsetAfterClose(t *testing.T) {
	logs := t.TempDir()
	writers, err := events.NewWriters(logs)
	if err != nil {
		t.Fatal(err)
	}
	defer writers.Close()
	bus := events.NewBus()
	profile := &config.Profile{ID: "main", Label: "Coder", Context: config.Context{NCtx: 32768, ReserveOutput: 8192}, Capabilities: config.Capabilities{Streaming: true, ToolCalls: true, OverflowBehavior: "error"}}
	cfg := config.Config{Context: config.GlobalContext{Accounting: "estimated"}}
	registry := NewRegistry(bus, writers, func(id string) (*config.Profile, bool) { return profile, id == profile.ID }, 40, func() config.Config { return cfg })
	first, err := registry.Create("", profile.ID, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first.ToggleTool("shell", false)
	if err := registry.Close(first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := registry.CreateLike(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	want, got := first.Snapshot(), second.Snapshot()
	if got.ServerID != want.ServerID || got.Workspace != want.Workspace || got.AgentName != "Coder" || got.MainProfile != "Coder" || got.Tools[5].Enabled {
		t.Fatalf("cloned session=%+v", got)
	}
	if len(registry.List()) != 2 || !registry.List()[0].Snapshot().Closed {
		t.Fatalf("closed session was discarded: %+v", registry.List())
	}
}
