package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
)

func TestFileToolsUseScratchPlusEveryPlanRepoAndKeepPlanFilesBound(t *testing.T) {
	root := t.TempDir()
	scratch := filepath.Join(root, "scratch")
	plans := filepath.Join(root, "plans")
	own, other := filepath.Join(plans, "own"), filepath.Join(plans, "other")
	repoA, repoB := filepath.Join(root, "repo-a"), filepath.Join(root, "repo-b")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{scratch, own, other, repoA, repoB, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	otherFile := filepath.Join(other, "plan.md")
	if err := os.WriteFile(otherFile, []byte("# Other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	enabled := map[string]bool{"read_file": true, "write_file": true}
	repos := func() []string { return []string{repoA, repoB} }
	b := &session.Session{ID: "b", Role: "b", Workspace: scratch, PlansRoot: plans, PlanRepos: repos, LastSeen: map[string]time.Time{}, ToolsEnabled: enabled}
	d := &session.Session{ID: "d", Role: "d", Workspace: repoA, PlansRoot: plans, PlanDir: own, PlanRepo: repoA, PlanRepos: repos, LastSeen: map[string]time.Time{}, ToolsEnabled: enabled}
	reader := NewReadFile(config.ReadFileTool{DefaultLimit: 4096, MaxLimit: 8192})
	coordinator := NewFileCoordinator(session.NewWorkspaceRegistry(), func(id string) string { return id }, events.NewBus())
	writer := NewWriteFile(coordinator)
	for name, item := range map[string]*session.Session{"b": b, "d": d} {
		result, err := reader.Call(context.Background(), item, map[string]any{"path": otherFile})
		if err != nil || !strings.Contains(result, "Other") {
			t.Fatalf("%s read result=%q err=%v", name, result, err)
		}
		if _, err := writer.Call(context.Background(), item, map[string]any{"path": otherFile, "content": "changed"}); err == nil {
			t.Fatalf("%s wrote sibling plan", name)
		}
	}
	for _, target := range []string{filepath.Join(repoA, "a.txt"), filepath.Join(repoB, "b.txt")} {
		if _, err := writer.Call(context.Background(), b, map[string]any{"path": target, "content": "ok\n"}); err != nil {
			t.Fatalf("b write plan repo %q: %v", target, err)
		}
	}
	if _, err := writer.Call(context.Background(), b, map[string]any{"path": filepath.Join(outside, "no.txt"), "content": "no\n"}); err == nil || !strings.Contains(err.Error(), "path is outside the folder") {
		t.Fatalf("outside-union write err=%v", err)
	}
	ownFile := filepath.Join(own, "plan.md")
	if _, err := writer.Call(context.Background(), d, map[string]any{"path": ownFile, "content": "# Mine\n"}); err != nil {
		t.Fatalf("bound write: %v", err)
	}
}

func TestWritingRootAgentFileTriggersPlanDetection(t *testing.T) {
	root := t.TempDir()
	called := ""
	item := &session.Session{ID: "b", Role: "b", Workspace: root, LastSeen: map[string]time.Time{}, EnsurePlan: func(dir string) { called = dir }}
	coordinator := NewFileCoordinator(session.NewWorkspaceRegistry(), func(id string) string { return id }, events.NewBus())
	if _, err := NewWriteFile(coordinator).Call(context.Background(), item, map[string]any{"path": "AGENT_B.md", "content": "instructions\n"}); err != nil {
		t.Fatal(err)
	}
	if called != root {
		t.Fatalf("detected folder=%q want %q", called, root)
	}
}
