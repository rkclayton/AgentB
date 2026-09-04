package tools

import (
	"context"
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
	return NewRemember(manager, events.NewBus()), NewRecall(manager), &session.Session{ID: "memory-test", Workspace: workspace}, baseDir
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
