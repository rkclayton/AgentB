package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/config"
)

func testManager(t *testing.T, baseDir, workspace string) *Manager {
	t.Helper()
	cfg := config.Defaults(workspace)
	cfg.Memory.Dir = "memory"
	return New(baseDir, func() config.Config { return cfg }, func(context.Context, string, string) (int, error) {
		return 0, nil
	})
}

func TestReadReturnsNotedEntriesAcrossManagerRestart(t *testing.T) {
	baseDir := t.TempDir()
	workspace := filepath.Join(baseDir, "workspace")
	first := testManager(t, baseDir, workspace)
	if _, duplicate, err := first.Note(workspace, "run the full Go test suite"); err != nil || duplicate {
		t.Fatalf("Note() = duplicate %v, error %v", duplicate, err)
	}

	content, err := first.Read(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "- ") || !strings.Contains(content, " run the full Go test suite") {
		t.Fatalf("Read() = %q, want dated stored entry", content)
	}

	restarted := testManager(t, baseDir, workspace)
	afterRestart, err := restarted.Read(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if afterRestart != content {
		t.Fatalf("Read() after manager restart = %q, want %q", afterRestart, content)
	}
}

func TestReadEmptyStoreReturnsCleanly(t *testing.T) {
	baseDir := t.TempDir()
	workspace := filepath.Join(baseDir, "workspace")
	manager := testManager(t, baseDir, workspace)
	content, err := manager.Read(workspace)
	if err != nil || content != "" {
		t.Fatalf("Read() = %q, %v; want empty success", content, err)
	}
	if err := os.MkdirAll(filepath.Dir(manager.Path(workspace)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.Path(workspace), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	content, err = manager.Read(workspace)
	if err != nil || content != "" {
		t.Fatalf("Read() of empty file = %q, %v; want empty success", content, err)
	}
}

func TestPathAlwaysStaysInConfiguredMemoryDirectory(t *testing.T) {
	baseDir := t.TempDir()
	memoryDir := filepath.Join(baseDir, "memory")
	cfg := config.Defaults(baseDir)
	cfg.Memory.Dir = memoryDir
	manager := New(baseDir, func() config.Config { return cfg }, nil)

	for _, workspace := range []string{baseDir, filepath.Join(baseDir, "..", "outside"), filepath.VolumeName(baseDir) + string(filepath.Separator)} {
		path := manager.Path(workspace)
		relative, err := filepath.Rel(memoryDir, path)
		if err != nil {
			t.Fatal(err)
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			t.Fatalf("Path(%q) escaped memory directory: %q", workspace, path)
		}
	}
}
