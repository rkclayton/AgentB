package session

import (
	"os"
	"path/filepath"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
	workspaceinfo "harness/internal/workspace"
)

func testPlanRegistry(t *testing.T) (*Registry, string, string) {
	t.Helper()
	root := t.TempDir()
	logs := filepath.Join(root, "logs")
	data := filepath.Join(root, "data")
	writers, err := events.NewWriters(logs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writers.Close() })
	profile := &config.Profile{ID: "main", Label: "Coder", Context: config.Context{NCtx: 32768, ReserveOutput: 8192}, Capabilities: config.Capabilities{Streaming: true, ToolCalls: true, OverflowBehavior: "error"}}
	cfg := config.Defaults(root)
	cfg.Agents = []config.Agent{{Name: "Coder", B: profile.ID, D: profile.ID, Toolset: config.FullToolset()}}
	registry := NewRegistry(events.NewBus(), writers, func(id string) (*config.Profile, bool) { return profile, id == profile.ID }, 40, func() config.Config { return cfg })
	registry.SetWorkspaceManager(workspaceinfo.New(data, func(dir string) string { return filepath.Join(data, filepath.Base(dir)+".md") }))
	registry.SetPlansRoot(filepath.Join(data, "plans"))
	return registry, data, config.AgentID("Coder")
}

func TestChatWithoutFolderOwnsScratch(t *testing.T) {
	registry, data, profile := testPlanRegistry(t)
	item, err := registry.Create("", profile, "")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(data, "scratch", item.ID)
	if !item.Scratch || item.Workspace != want {
		t.Fatalf("scratch=%v folder=%q want %q", item.Scratch, item.Workspace, want)
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("scratch folder: %v", err)
	}
}

func TestScratchNeverReusesAnOrphanedChatFolder(t *testing.T) {
	registry, data, profile := testPlanRegistry(t)
	orphan := filepath.Join(data, "scratch", "main")
	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "retained.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	item, err := registry.Create("", profile, "")
	if err != nil {
		t.Fatal(err)
	}
	if item.ID == "main" || item.Workspace == orphan || item.Label != item.ID {
		t.Fatalf("scratch reused orphan: id=%q label=%q folder=%q", item.ID, item.Label, item.Workspace)
	}
	if got, err := os.ReadFile(filepath.Join(orphan, "retained.txt")); err != nil || string(got) != "keep" {
		t.Fatalf("orphan changed: %q %v", got, err)
	}
}

func TestPlanRepoSelectionAndCanonicalDetection(t *testing.T) {
	registry, _, profile := testPlanRegistry(t)
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := registry.Create("", profile, repo)
	if err != nil {
		t.Fatal(err)
	}
	if first.Scratch {
		t.Fatal("folder chat marked scratch")
	}
	plans := registry.planListLocked()
	if len(plans) != 1 || !samePath(plans[0].Repo, repo) || plans[0].Name != "repo" {
		t.Fatalf("plans=%+v", plans)
	}
	if _, err := registry.Create("", profile, filepath.Join(repo, ".")); err != nil {
		t.Fatal(err)
	}
	if got := len(registry.planListLocked()); got != 1 {
		t.Fatalf("duplicate plans=%d", got)
	}
	d, err := registry.CreateRole("", profile, "", "d", plans[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Workspace != repo || d.PlanRepo != repo {
		t.Fatalf("plan-selected folder=%q repo=%q", d.Workspace, d.PlanRepo)
	}
}

func TestRetainedChatRestoresPreviousFolder(t *testing.T) {
	registry, _, profile := testPlanRegistry(t)
	repo := t.TempDir()
	created, err := registry.Create("", profile, repo)
	if err != nil {
		t.Fatal(err)
	}
	saved := created.Snapshot()
	registry2, _, _ := testPlanRegistry(t)
	restored, err := registry2.Restore(saved)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Workspace != repo || restored.Scratch {
		t.Fatalf("restored folder=%q scratch=%v", restored.Workspace, restored.Scratch)
	}
}

func TestEnsurePlanImmediatelyAddsRepoToBSessionUnion(t *testing.T) {
	registry, _, profile := testPlanRegistry(t)
	item, err := registry.Create("", profile, "")
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "approved-repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	plan, created, err := registry.EnsurePlan(repo)
	if err != nil || !created || !samePath(plan.Repo, repo) {
		t.Fatalf("plan=%+v created=%v err=%v", plan, created, err)
	}
	if root, err := item.WriteRoot(filepath.Join(repo, "ready.txt")); err != nil || !samePath(root, repo) {
		t.Fatalf("new repo root=%q err=%v", root, err)
	}
}

func TestPlansTreeReadEverywhereWriteOnlyBoundPlan(t *testing.T) {
	root := t.TempDir()
	plans := filepath.Join(root, "plans")
	own := filepath.Join(plans, "own")
	other := filepath.Join(plans, "other")
	for _, dir := range []string{own, other} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	b := &Session{Role: "b", Workspace: filepath.Join(root, "repo"), PlansRoot: plans}
	d := &Session{Role: "d", Workspace: filepath.Join(root, "repo"), PlansRoot: plans, PlanDir: own}
	for name, item := range map[string]*Session{"b": b, "d": d} {
		if got, err := item.ReadRoot(filepath.Join(other, "plan.md")); err != nil || got != plans {
			t.Fatalf("%s read root=%q err=%v", name, got, err)
		}
		if _, err := item.WriteRoot(filepath.Join(other, "plan.md")); err == nil {
			t.Fatalf("%s wrote sibling plan", name)
		}
	}
	if got, err := d.WriteRoot(filepath.Join(own, "plan.md")); err != nil || got != own {
		t.Fatalf("bound write root=%q err=%v", got, err)
	}
}
