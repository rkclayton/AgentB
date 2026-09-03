package tools

import (
	"bytes"
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

func testSession(root, id, label string) *session.Session {
	return &session.Session{ID: id, Label: label, Workspace: root, LastSeen: map[string]time.Time{}, ToolsEnabled: map[string]bool{"read_file": true, "write_file": true, "edit_file": true}}
}
func testTools(root string) (*EditFile, *WriteFile, *ReadFile, *session.WorkspaceRegistry) {
	workspaces := session.NewWorkspaceRegistry()
	labels := map[string]string{"a": "A", "b": "B"}
	coordinator := NewFileCoordinator(workspaces, func(id string) string { return labels[id] }, events.NewBus())
	return NewEditFile(coordinator), NewWriteFile(coordinator), NewReadFile(config.Defaults(root).Tools.ReadFile), workspaces
}
func writeFixture(t *testing.T, root, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
func edit(t *testing.T, tool *EditFile, s *session.Session, path, old, replacement string) (string, error) {
	t.Helper()
	return tool.Call(context.Background(), s, map[string]any{"path": path, "old_string": old, "new_string": replacement})
}

func TestEditFile(t *testing.T) {
	t.Run("exact_unique", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("one\ntwo\nthree\n"))
		got, err := edit(t, tool, s, path, "two", "TWO")
		if err != nil || !strings.HasPrefix(got, "ok: replaced lines") {
			t.Fatalf("got %q, %v", got, err)
		}
		data, _ := os.ReadFile(path)
		if string(data) != "one\nTWO\nthree\n" {
			t.Fatalf("file %q", data)
		}
	})
	t.Run("multiple", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("same\na\nsame\nb\nsame\n"))
		before, _ := os.ReadFile(path)
		_, err := edit(t, tool, s, path, "same", "other")
		if err == nil || !strings.Contains(err.Error(), "matches 3 places") || !strings.Contains(err.Error(), "lines 1, 3, 5") {
			t.Fatalf("error %v", err)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatal("file modified")
		}
	})
	t.Run("trailing_whitespace", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("alpha   \nbeta\n"))
		got, err := edit(t, tool, s, path, "alpha\nbeta", "A\nB")
		if err != nil || !strings.Contains(got, "trailing-whitespace normalization") {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("dedented_old_string", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.go", []byte("        one\n        two\n"))
		got, err := edit(t, tool, s, path, "    one\n    two", "    ONE\n        child")
		if err != nil || !strings.Contains(got, "indentation adjusted (+4 spaces)") {
			t.Fatalf("got %q, %v", got, err)
		}
		data, _ := os.ReadFile(path)
		if !strings.Contains(string(data), "        ONE\n            child") {
			t.Fatalf("file %q", data)
		}
	})
	t.Run("tabs_vs_spaces", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.go", []byte("\tone\n\t\ttwo\n"))
		got, err := edit(t, tool, s, path, "    one\n        two", "    ONE\n        TWO")
		if err != nil || !strings.Contains(got, "tabs") {
			t.Fatalf("got %q, %v", got, err)
		}
		data, _ := os.ReadFile(path)
		if !strings.Contains(string(data), "\tONE\n\t\tTWO") {
			t.Fatalf("file %q", data)
		}
	})
	t.Run("CRLF_preserved", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("a\r\nb\r\nc\r\n"))
		_, err := edit(t, tool, s, path, "b", "B")
		if err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path)
		stripped := bytes.ReplaceAll(data, []byte("\r\n"), nil)
		if bytes.Contains(stripped, []byte("\n")) {
			t.Fatalf("lone LF in %q", data)
		}
	})
	t.Run("BOM_preserved", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", append([]byte{0xef, 0xbb, 0xbf}, []byte("hello\n")...))
		_, err := edit(t, tool, s, path, "hello", "world")
		if err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path)
		if !bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
			t.Fatal("BOM lost")
		}
	})
	t.Run("no_trailing_newline", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("a\nlast"))
		_, err := edit(t, tool, s, path, "last", "LAST")
		if err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path)
		if bytes.HasSuffix(data, []byte("\n")) {
			t.Fatal("trailing newline added")
		}
	})
	t.Run("deletion", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("a\nb\nc"))
		got, err := edit(t, tool, s, path, "b\n", "")
		if err != nil || !strings.HasPrefix(got, "ok: deleted lines") {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("near_miss_at_least_0_6", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("one\nchanged file line\nthree\n"))
		_, err := edit(t, tool, s, path, "one\nexpected old line\nthree", "x")
		if err == nil || !strings.Contains(err.Error(), "Closest match: lines") || !strings.Contains(err.Error(), `"changed file line"`) || !strings.Contains(err.Error(), `"expected old line"`) {
			t.Fatalf("error %v", err)
		}
	})
	t.Run("near_miss_below_0_6", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("alpha\nbeta\ngamma\n"))
		_, err := edit(t, tool, s, path, "totally\nunrelated\ncontent", "x")
		if err == nil || !strings.Contains(err.Error(), "no similar region") {
			t.Fatalf("error %v", err)
		}
	})
	t.Run("already_applied", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("new value\n"))
		_, err := edit(t, tool, s, path, "old value", "new value")
		if err == nil || !strings.Contains(err.Error(), "may already be applied") {
			t.Fatalf("error %v", err)
		}
	})
	t.Run("empty_old_string", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("x"))
		_, err := edit(t, tool, s, path, "", "new")
		if err == nil || !strings.Contains(err.Error(), "use write_file") {
			t.Fatalf("error %v", err)
		}
	})
	t.Run("unicode", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("café\nhello 🌍\n"))
		got, err := edit(t, tool, s, path, "hello 🌍", "hello 🚀")
		if err != nil || !strings.Contains(got, "lines 2–2") {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("outside_workspace", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		for _, path := range []string{"../x.go", filepath.Join(filepath.Dir(root), "other.go")} {
			_, err := edit(t, tool, s, path, "a", "b")
			if err == nil || !strings.Contains(err.Error(), "path is outside the workspace") {
				t.Fatalf("path %q error %v", path, err)
			}
		}
	})
	t.Run("binary", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.bin", []byte{'a', 0, 'b'})
		_, err := edit(t, tool, s, path, "a", "x")
		if err == nil || !strings.Contains(err.Error(), "binary") {
			t.Fatalf("error %v", err)
		}
	})
	t.Run("stale_view", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("old"))
		s.Touch("x.txt")
		future := time.Now().Add(2 * time.Second)
		if err := os.Chtimes(path, future, future); err != nil {
			t.Fatal(err)
		}
		got, err := edit(t, tool, s, path, "old", "new")
		if err != nil || !strings.HasPrefix(got, "note: file changed since you last read it.\n") {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("conflict_refused", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, workspaces := testTools(root)
		a := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("old"))
		a.Touch("x.txt")
		time.Sleep(time.Millisecond)
		workspaces.RecordWrite(root, "x.txt", "b")
		_, err := edit(t, tool, a, path, "old", "new")
		if err == nil || !strings.Contains(err.Error(), "session B wrote this file") || !strings.Contains(err.Error(), "re-read before editing") {
			t.Fatalf("error %v", err)
		}
		data, _ := os.ReadFile(path)
		if string(data) != "old" {
			t.Fatal("file modified")
		}
	})
	t.Run("conflict_cleared", func(t *testing.T) {
		root := t.TempDir()
		tool, _, reader, workspaces := testTools(root)
		a := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("old"))
		a.Touch("x.txt")
		time.Sleep(time.Millisecond)
		workspaces.RecordWrite(root, "x.txt", "b")
		if _, err := reader.Call(context.Background(), a, map[string]any{"path": path}); err != nil {
			t.Fatal(err)
		}
		got, err := edit(t, tool, a, path, "old", "new")
		if err != nil || !strings.HasPrefix(got, "ok: replaced lines") {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("write_file_conflict", func(t *testing.T) {
		root := t.TempDir()
		_, writer, _, workspaces := testTools(root)
		a := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("old"))
		a.Touch("x.txt")
		time.Sleep(time.Millisecond)
		workspaces.RecordWrite(root, "x.txt", "b")
		_, err := writer.Call(context.Background(), a, map[string]any{"path": path, "content": "new"})
		if err == nil || !strings.Contains(err.Error(), "session B wrote this file") {
			t.Fatalf("error %v", err)
		}
		data, _ := os.ReadFile(path)
		if string(data) != "old" {
			t.Fatal("file modified")
		}
	})
}
