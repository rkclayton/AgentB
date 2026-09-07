package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func TestCanonicalWorkspaceMemoryKeyAndLegacyDefaultMigration(t *testing.T) {
	baseDir := t.TempDir()
	workspace := filepath.Join(baseDir, "MixedCaseWorkspace")
	manager := testManager(t, baseDir, workspace)
	canonical := manager.Path(workspace)
	if filepath.Separator == '\\' {
		alternate := strings.ToUpper(workspace)
		if got := manager.Path(alternate); !strings.EqualFold(filepath.Base(got), filepath.Base(canonical)) {
			t.Fatalf("case spelling changed key: %q vs %q", got, canonical)
		}
		clean, _ := filepath.Abs(workspace)
		legacySum := sha256.Sum256([]byte(filepath.Clean(clean)))
		legacy := filepath.Join(filepath.Dir(canonical), filepath.Base(clean)+"-"+hex.EncodeToString(legacySum[:4])+".md")
		if legacy != canonical {
			if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(legacy, []byte("- 2026-09-07 existing default memory\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := manager.Path(workspace); got != legacy {
				t.Fatalf("legacy migration path=%q want %q", got, legacy)
			}
			content, err := manager.Read(workspace)
			if err != nil || !strings.Contains(content, "existing default memory") {
				t.Fatalf("content=%q err=%v", content, err)
			}
		}
	}
	if _, _, err := manager.Note(workspace, "clear me"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Clear(workspace); err != nil {
		t.Fatal(err)
	}
	if content, err := manager.Read(workspace); err != nil || content != "" {
		t.Fatalf("after clear=%q err=%v", content, err)
	}
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
