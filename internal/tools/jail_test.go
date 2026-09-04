package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJail(t *testing.T) {
	t.Run("symlink_escape_rejected", func(t *testing.T) {
		root, outside := t.TempDir(), t.TempDir()
		link := filepath.Join(root, "escape")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		_, err := Resolve(root, filepath.Join("escape", "x.txt"))
		if err == nil || !strings.Contains(err.Error(), "outside the workspace") {
			t.Fatalf("error %v", err)
		}
	})
	t.Run("absolute_inside_accepted", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "dir", "x.txt")
		got, err := Resolve(root, path)
		if err != nil || got != filepath.Clean(path) {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("dotdot_stays_inside", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "dir"), 0o755); err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(root, "x.txt")
		got, err := Resolve(root, filepath.Join("dir", "..", "x.txt"))
		if err != nil || got != want {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("os_authorized_path_does_not_probe_workspace_ancestors", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "workspace-that-does-not-exist")
		target := filepath.Join(t.TempDir(), "allowed.txt")
		got, err := resolvePath(root, target, false)
		if err != nil || got != filepath.Clean(target) {
			t.Fatalf("got %q, %v", got, err)
		}
	})
}
