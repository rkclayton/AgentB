package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
	workspaceinfo "harness/internal/workspace"
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

func TestCreateLikeAtBindsCanonicalDirectoryAndMissingIsVisible(t *testing.T) {
	logs, data, source := t.TempDir(), t.TempDir(), t.TempDir()
	writers, err := events.NewWriters(logs)
	if err != nil {
		t.Fatal(err)
	}
	defer writers.Close()
	profile := &config.Profile{ID: "main", Label: "Coder", Context: config.Context{NCtx: 32768, ReserveOutput: 8192}, Capabilities: config.Capabilities{Streaming: true, ToolCalls: true, OverflowBehavior: "error"}}
	cfg := config.Defaults(source)
	registry := NewRegistry(events.NewBus(), writers, func(id string) (*config.Profile, bool) { return profile, id == profile.ID }, 40, func() config.Config { return cfg })
	manager := workspaceinfo.New(data, func(dir string) string { return filepath.Join(data, filepath.Base(dir)+".md") })
	registry.SetWorkspaceManager(manager)
	first, err := registry.Create("", profile.ID, source)
	if err != nil {
		t.Fatal(err)
	}
	bound := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(bound, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bound, "AGENTS.md"), []byte("project rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := registry.CreateLikeAt(first.ID, filepath.Join(bound, "."))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := second.Snapshot()
	if snapshot.WorkspaceDir != filepath.Clean(bound) || snapshot.Workspace != snapshot.WorkspaceDir || snapshot.WorkspaceMissing || !strings.Contains(snapshot.ProjectContent, "project rule") {
		t.Fatalf("bound snapshot=%+v", snapshot)
	}
	missing := filepath.Join(t.TempDir(), "absent")
	third, err := registry.CreateLikeAt(first.ID, missing)
	if err != nil {
		t.Fatal(err)
	}
	if !third.Snapshot().WorkspaceMissing {
		t.Fatalf("missing snapshot=%+v", third.Snapshot())
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("missing directory was created: %v", err)
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

func TestRenameAuthorsPinUserAndLeaveAuxUnpinned(t *testing.T) {
	logs := t.TempDir()
	writers, err := events.NewWriters(logs)
	if err != nil {
		t.Fatal(err)
	}
	defer writers.Close()
	bus := events.NewBus()
	profile := &config.Profile{ID: "main", Label: "Coder", Context: config.Context{NCtx: 32768, ReserveOutput: 8192}, Capabilities: config.Capabilities{Streaming: true, ToolCalls: true, OverflowBehavior: "error"}}
	cfg := config.Defaults(t.TempDir())
	registry := NewRegistry(bus, writers, func(id string) (*config.Profile, bool) { return profile, id == profile.ID }, 40, func() config.Config { return cfg })
	item, err := registry.Create("main", profile.ID, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	eventsOut, cancel := bus.Subscribe()
	defer cancel()
	if err := registry.RenameBy(item.ID, "Aux suggestion", "aux"); err != nil {
		t.Fatal(err)
	}
	aux := <-eventsOut
	if aux.Type != events.SessionRenamed || aux.Data.(map[string]any)["by"] != "aux" || item.Snapshot().NamePinned {
		t.Fatalf("aux event=%+v snapshot=%+v", aux, item.Snapshot())
	}
	if err := registry.Rename(item.ID, "User title"); err != nil {
		t.Fatal(err)
	}
	user := <-eventsOut
	if user.Data.(map[string]any)["by"] != "user" || !item.Snapshot().NamePinned {
		t.Fatalf("user event=%+v", user)
	}
	if err := registry.RenameBy(item.ID, "Ignored", "aux"); err != nil {
		t.Fatal(err)
	}
	if item.Snapshot().Label != "User title" {
		t.Fatalf("pin lost: %+v", item.Snapshot())
	}
	if err := registry.Close(item.ID); err != nil {
		t.Fatal(err)
	}
	if err := registry.Rename(item.ID, "Closed title"); err != nil {
		t.Fatal(err)
	}
	if item.Snapshot().Label != "Closed title" {
		t.Fatalf("closed rename=%+v", item.Snapshot())
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
