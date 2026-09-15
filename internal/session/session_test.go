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

func TestCreateLikePreservesToolsetButCreatesFreshScratch(t *testing.T) {
	logs, data, source := t.TempDir(), t.TempDir(), t.TempDir()
	writers, err := events.NewWriters(logs)
	if err != nil {
		t.Fatal(err)
	}
	defer writers.Close()
	profile := &config.Profile{ID: "main", Label: "Coder", Context: config.Context{NCtx: 32768, ReserveOutput: 8192}, Capabilities: config.Capabilities{Streaming: true, ToolCalls: true, OverflowBehavior: "error"}}
	cfg := config.Defaults(source)
	registry := NewRegistry(events.NewBus(), writers, func(id string) (*config.Profile, bool) { return profile, id == profile.ID }, 40, func() config.Config { return cfg })
	registry.SetPlansRoot(filepath.Join(data, "plans"))
	manager := workspaceinfo.New(data, func(dir string) string { return filepath.Join(data, filepath.Base(dir)+".md") })
	registry.SetWorkspaceManager(manager)
	first, err := registry.Create("", profile.ID, source)
	if err != nil {
		t.Fatal(err)
	}
	first.ToggleTool("shell", true)
	second, err := registry.CreateLike(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := second.Snapshot()
	if !snapshot.Scratch || snapshot.WorkspaceDir == first.Snapshot().WorkspaceDir || !strings.HasPrefix(snapshot.WorkspaceDir, filepath.Join(data, "scratch")) {
		t.Fatalf("scratch snapshot=%+v", snapshot)
	}
	if !second.EnabledTools()["shell"] {
		t.Fatal("source toolset was not preserved")
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
	held := &Session{Run: RunState{Status: "held"}}
	if err := held.Close(); err == nil || held.IsClosed() {
		t.Fatalf("held close err=%v closed=%t", err, held.IsClosed())
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
	registry.SetPlansRoot(filepath.Join(t.TempDir(), "plans"))
	item, err := registry.Create("main", profile.ID, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	eventsOut, cancel := bus.Subscribe()
	defer cancel()
	if err := registry.RenameBy(item.ID, "C suggestion", "c"); err != nil {
		t.Fatal(err)
	}
	aux := <-eventsOut
	if aux.Type != events.SessionRenamed || aux.Data.(map[string]any)["by"] != "c" || item.Snapshot().NamePinned {
		t.Fatalf("aux event=%+v snapshot=%+v", aux, item.Snapshot())
	}
	if err := registry.Rename(item.ID, "User title"); err != nil {
		t.Fatal(err)
	}
	user := <-eventsOut
	if user.Data.(map[string]any)["by"] != "user" || !item.Snapshot().NamePinned {
		t.Fatalf("user event=%+v", user)
	}
	if err := registry.RenameBy(item.ID, "Ignored", "c"); err != nil {
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
	registry.SetPlansRoot(filepath.Join(t.TempDir(), "plans"))
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
	if got.AgentID != "coder" || got.ServerID != want.ServerID || !got.Scratch || got.Workspace == want.Workspace || got.AgentName != "Coder" || got.BProfile != "Coder" || got.Tools[5].Enabled {
		t.Fatalf("cloned session=%+v", got)
	}
	if len(registry.List()) != 2 || !registry.List()[0].Snapshot().Closed {
		t.Fatalf("closed session was discarded: %+v", registry.List())
	}
}

func TestRoleAndPlanSnapshotRestoreWithoutLegacyMigration(t *testing.T) {
	logs := t.TempDir()
	writers, err := events.NewWriters(logs)
	if err != nil {
		t.Fatal(err)
	}
	defer writers.Close()
	profile := &config.Profile{ID: "planner", Label: "Planner", Context: config.Context{NCtx: 32768, ReserveOutput: 8192}, Capabilities: config.Capabilities{Streaming: true, ToolCalls: true, OverflowBehavior: "error"}}
	cfg := config.Config{Context: config.GlobalContext{Accounting: "estimated"}, Agents: []config.Agent{{Name: "Agent", B: "planner", D: "planner", Toolset: config.FullToolset()}}}
	registry := NewRegistry(events.NewBus(), writers, func(id string) (*config.Profile, bool) { return profile, id == profile.ID }, 40, func() config.Config { return cfg })
	plans := t.TempDir()
	registry.SetPlansRoot(plans)
	legacy, err := registry.Create("legacy", "agent", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	legacySaved := legacy.Snapshot()
	legacySaved.Role = ""
	legacySaved.ID = "legacy-restored"
	if restored, err := registry.Restore(legacySaved); err != nil || restored.Snapshot().Role != "b" || restored.Snapshot().PlanID != "" {
		t.Fatalf("legacy restore=%+v err=%v", restored, err)
	}
	planID := "stable-plan"
	planDir := filepath.Join(plans, planID)
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "plan.md"), []byte("# Stable name\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.CreateRole("invalid", "agent", legacy.Workspace, "d", "."); err == nil || !strings.Contains(err.Error(), "invalid plan id") {
		t.Fatalf("invalid plan id error=%v", err)
	}
	d, err := registry.CreateRole("design", "agent", legacy.Workspace, "d", planID)
	if err != nil {
		t.Fatal(err)
	}
	saved := d.Snapshot()
	saved.ID = "design-restored"
	restored, err := registry.Restore(saved)
	if err != nil {
		t.Fatal(err)
	}
	got := restored.Snapshot()
	if got.Role != "d" || got.PlanID != planID || got.PlanName != "Stable name" || got.PlanDir != planDir {
		t.Fatalf("d restore=%+v", got)
	}
	if restored.ToggleTool("shell", true) || restored.ToolEnabled("shell") {
		t.Fatal("d session enabled shell outside its file jail")
	}
	d.ToolsEnabled["shell"] = true
	d.ApplyAgentConfig("agent", cfg.Agents[0], *profile)
	if d.ServerID != cfg.Agents[0].D || d.ToolEnabled("shell") || d.ToolEnabled("run_script") {
		t.Fatalf("agent rebind widened d session: %+v", d.Snapshot())
	}
}
