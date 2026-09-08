package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/memory"
	"harness/internal/session"
)

func memoryTools(t *testing.T) (*Remember, *Recall, *session.Session, string) {
	t.Helper()
	baseDir := t.TempDir()
	workspace := filepath.Join(baseDir, "workspace")
	cfg := config.Defaults(workspace)
	cfg.Memory.Dir = filepath.Join(baseDir, "memory")
	manager := memory.New(baseDir, func() config.Config { return cfg }, nil)
	return NewRemember(manager, events.NewBus()), NewRecall(manager), &session.Session{ID: "memory-test", AgentID: "coder", Workspace: workspace}, baseDir
}

func TestRememberTargetSeparatesWorkspaceAndAgentMemory(t *testing.T) {
	remember, recall, item, _ := memoryTools(t)
	if _, err := remember.Call(context.Background(), item, map[string]any{"note": "project fact", "target": "workspace"}); err != nil {
		t.Fatal(err)
	}
	if _, err := remember.Call(context.Background(), item, map[string]any{"note": "operator preference", "target": "agent"}); err != nil {
		t.Fatal(err)
	}
	value, err := recall.Call(context.Background(), item, nil)
	if err != nil || !strings.Contains(value, "Workspace memory:\n") || !strings.Contains(value, "project fact") || !strings.Contains(value, "Agent memory:\n") || !strings.Contains(value, "operator preference") {
		t.Fatalf("recall=%q err=%v", value, err)
	}
	properties := remember.Schema()["properties"].(map[string]any)
	target := properties["target"].(map[string]any)
	if target["default"] != "workspace" {
		t.Fatalf("target schema=%#v", target)
	}
}

func TestRecallReadsRememberEntryAndRememberStillDetectsDuplicate(t *testing.T) {
	remember, recall, item, _ := memoryTools(t)
	ctx := context.Background()
	result, err := remember.Call(ctx, item, map[string]any{"note": "prefer focused tests first"})
	if err != nil || result != "ok: noted; active next session." {
		t.Fatalf("remember.Call() = %q, %v", result, err)
	}
	result, err = recall.Call(ctx, item, nil)
	if err != nil || !strings.Contains(result, " prefer focused tests first") {
		t.Fatalf("recall.Call() = %q, %v", result, err)
	}
	result, err = remember.Call(ctx, item, map[string]any{"note": "prefer focused tests first"})
	if err != nil || result != "ok: already noted" {
		t.Fatalf("duplicate remember.Call() = %q, %v", result, err)
	}
}

func TestRecallEmptyStoreIgnoresModelSuppliedPath(t *testing.T) {
	_, recall, item, baseDir := memoryTools(t)
	outside := filepath.Join(baseDir, "outside.md")
	if err := os.WriteFile(outside, []byte("outside secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := recall.Call(context.Background(), item, map[string]any{"path": outside})
	if err != nil || result != "No saved notes for this workspace." {
		t.Fatalf("recall.Call() = %q, %v", result, err)
	}
	if strings.Contains(result, "outside secret") {
		t.Fatal("recall read model-supplied path")
	}
}

func TestRecallSchemaExposesNoPath(t *testing.T) {
	_, recall, _, _ := memoryTools(t)
	properties, ok := recall.Schema()["properties"].(map[string]any)
	if !ok || len(properties) != 0 {
		t.Fatalf("recall properties = %#v, want none", recall.Schema()["properties"])
	}
}

func TestRememberToolsBlockByteDelta(t *testing.T) {
	legacy := map[string]any{"type": "function", "function": map[string]any{
		"name": "remember", "description": "Save note as durable workspace memory for future sessions. Call recall first to avoid duplicates; unlike recall, remember writes.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{"note": map[string]any{"type": "string"}}, "required": []string{"note"}},
	}}
	current := map[string]any{"type": "function", "function": map[string]any{
		"name": "remember", "description": (&Remember{}).Description(), "parameters": (&Remember{}).Schema(),
	}}
	before, err := json.Marshal([]any{legacy})
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal([]any{current})
	if err != nil {
		t.Fatal(err)
	}
	const wantDelta = 111
	if delta := len(after) - len(before); delta != wantDelta {
		t.Fatalf("remember tools-block byte delta=%d, want %d", delta, wantDelta)
	}
	t.Logf("remember tools-block byte delta: +%d (%d to %d)", wantDelta, len(before), len(after))
}
