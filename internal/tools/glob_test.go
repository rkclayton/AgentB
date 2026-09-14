package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/session"
)

func TestGlob(t *testing.T) {
	t.Run("match_and_sort", func(t *testing.T) {
		root := t.TempDir()
		writeGlobFixture(t, root, "z.go")
		writeGlobFixture(t, root, "nested/b.go")
		writeGlobFixture(t, root, "nested/a.go")
		writeGlobFixture(t, root, "nested/no.txt")
		writeGlobFixture(t, root, "logs/hidden.go")
		writeGlobFixture(t, root, "memory/hidden.go")
		writeGlobFixture(t, root, "node_modules/hidden.go")
		writeGlobFixture(t, root, ".git/hidden.go")

		got, err := NewGlob(config.Defaults(root).Tools.FindFiles).Call(context.Background(), &session.Session{Workspace: root}, map[string]any{"pattern": "*.go"})
		if err != nil {
			t.Fatal(err)
		}
		want := "nested/a.go\nnested/b.go\nz.go"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		got, err = NewGlob(config.Defaults(root).Tools.FindFiles).Call(context.Background(), &session.Session{Workspace: root}, map[string]any{"pattern": "**/*.go"})
		if err != nil || got != want {
			t.Fatalf("recursive pattern got %q, %v; want %q", got, err, want)
		}
	})

	t.Run("no_match", func(t *testing.T) {
		root := t.TempDir()
		writeGlobFixture(t, root, "only.txt")
		got, err := NewGlob(config.Defaults(root).Tools.FindFiles).Call(context.Background(), &session.Session{Workspace: root}, map[string]any{"pattern": "*.go"})
		if err != nil || got != "no matches" {
			t.Fatalf("got %q, %v", got, err)
		}
	})

	t.Run("boundary_escape", func(t *testing.T) {
		root := t.TempDir()
		_, err := NewGlob(config.Defaults(root).Tools.FindFiles).Call(context.Background(), &session.Session{Workspace: root}, map[string]any{"pattern": "*", "path": ".."})
		if err == nil || !strings.Contains(err.Error(), "outside the folder") {
			t.Fatalf("error %v", err)
		}
	})

	t.Run("truncation_marker", func(t *testing.T) {
		root := t.TempDir()
		for i := 0; i < globMaxResults+3; i++ {
			writeGlobFixture(t, root, fmt.Sprintf("file-%03d.txt", i))
		}
		got, err := NewGlob(config.Defaults(root).Tools.FindFiles).Call(context.Background(), &session.Session{Workspace: root}, map[string]any{"pattern": "*.txt"})
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(got, "\n")
		if len(lines) != globMaxResults+1 {
			t.Fatalf("got %d output lines, want %d", len(lines), globMaxResults+1)
		}
		if lines[len(lines)-1] != "[truncated: 200 of 203 paths shown]" {
			t.Fatalf("missing explicit truncation marker: %q", lines[len(lines)-1])
		}
	})
}

func TestConfiguredProtectedRootsMatchVolumeRelativePrefixes(t *testing.T) {
	volumeRoot := string(filepath.Separator)
	if volume := filepath.VolumeName(t.TempDir()); volume != "" {
		volumeRoot = volume + string(filepath.Separator)
	}
	patterns := config.Defaults(t.TempDir()).Tools.FindFiles.SkipRoots
	for _, candidate := range []string{
		filepath.Join(volumeRoot, "Windows", "System32"),
		filepath.Join(volumeRoot, "$Recycle.Bin", "account"),
		filepath.Join(volumeRoot, "System Volume Information", "tracking.log"),
		filepath.Join(volumeRoot, "ProgramData", "Microsoft", "Windows Defender Advanced Threat Protection", "Classification"),
		filepath.Join(volumeRoot, "Program Files", "Windows Defender", "scan.dll"),
	} {
		if !configuredSkipRoot(candidate, patterns) {
			t.Errorf("protected path did not match: %s", candidate)
		}
	}
	if configuredSkipRoot(filepath.Join(volumeRoot, "ProgramData", "ordinary", "file.txt"), patterns) {
		t.Fatal("ordinary root matched protected patterns")
	}
}

func writeGlobFixture(t *testing.T, root, name string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
		t.Fatal(err)
	}
}
