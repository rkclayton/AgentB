package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	editor := NewEditFile(coordinator)
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
	manifest := filepath.Join(own, "plan.json")
	if err := os.WriteFile(manifest, []byte("{\"repo\":\"original\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := writer.Call(context.Background(), d, map[string]any{"path": manifest, "content": "{\"repo\":\"C:\\\\\"}\n"}); err == nil || !strings.Contains(err.Error(), "plan manifest immutability rule") || result != "" {
		t.Fatalf("d plan manifest write result=%q err=%v", result, err)
	}
	if result, err := editor.Call(context.Background(), d, map[string]any{"path": manifest, "old_string": "original", "new_string": "outside"}); err == nil || !strings.Contains(err.Error(), "plan manifest immutability rule") || result != "" {
		t.Fatalf("d plan manifest edit result=%q err=%v", result, err)
	}
	if data, err := os.ReadFile(manifest); err != nil || string(data) != "{\"repo\":\"original\"}\n" {
		t.Fatalf("plan manifest changed to %q err=%v", data, err)
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

func TestDUnionRootsRejectSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	plans := filepath.Join(root, "plans")
	own := filepath.Join(plans, "own")
	repo := filepath.Join(root, "repo")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{own, repo, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, parent := range []string{repo, own} {
		if err := linkTestDirectory(outside, filepath.Join(parent, "escape")); err != nil {
			t.Skipf("symlink creation is unavailable: %v", err)
		}
	}
	item := &session.Session{ID: "d", Role: "d", Workspace: repo, PlansRoot: plans, PlanDir: own, PlanRepo: repo, PlanRepos: func() []string { return []string{repo} }, LastSeen: map[string]time.Time{}, ToolsEnabled: map[string]bool{"read_file": true}}
	reader := NewReadFile(config.ReadFileTool{DefaultLimit: 4096, MaxLimit: 8192})
	for _, ctx := range []context.Context{context.Background(), withOSPathPolicy(context.Background())} {
		for _, target := range []string{filepath.Join(repo, "escape", "secret.txt"), filepath.Join(own, "escape", "secret.txt")} {
			if result, err := reader.Call(ctx, item, map[string]any{"path": target}); err == nil || result != "" {
				t.Fatalf("symlink escape %q result=%q err=%v", target, result, err)
			}
		}
	}
}

func TestRepoPolicyImmutabilityUsesResolvedTarget(t *testing.T) {
	root := t.TempDir()
	policyDir := filepath.Join(root, ".agentb")
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(policyDir, "policy.json")
	if err := os.WriteFile(policyPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := linkTestDirectory(policyDir, filepath.Join(root, "safe")); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	item := &session.Session{ID: "b", Role: "b", Workspace: root, LastSeen: map[string]time.Time{}, ToolsEnabled: map[string]bool{"write_file": true, "edit_file": true}}
	coordinator := NewFileCoordinator(session.NewWorkspaceRegistry(), func(id string) string { return id }, events.NewBus())
	if result, err := NewWriteFile(coordinator).Call(context.Background(), item, map[string]any{"path": filepath.Join("safe", "policy.json"), "content": "changed"}); err == nil || result != "" {
		t.Fatalf("write through policy link result=%q err=%v", result, err)
	}
	if result, err := NewEditFile(coordinator).Call(context.Background(), item, map[string]any{"path": filepath.Join("safe", "policy.json"), "old_string": "{}", "new_string": "changed"}); err == nil || result != "" {
		t.Fatalf("edit through policy link result=%q err=%v", result, err)
	}
	if data, err := os.ReadFile(policyPath); err != nil || string(data) != "{}" {
		t.Fatalf("policy changed to %q err=%v", data, err)
	}
}

func linkTestDirectory(target, link string) error {
	if err := os.Symlink(target, link); err == nil || runtime.GOOS != "windows" {
		return err
	}
	return exec.Command("cmd.exe", "/c", "mklink", "/J", link, target).Run()
}
