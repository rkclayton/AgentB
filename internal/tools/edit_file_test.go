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

func TestDSessionFileBoundarySeparatesPlanWritesFromRepositoryReads(t *testing.T) {
	root := t.TempDir()
	repo, plans := filepath.Join(root, "repo"), filepath.Join(root, "plans")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "source.txt"), []byte("repository"), 0o644); err != nil {
		t.Fatal(err)
	}
	edit, write, read, _ := testTools(repo)
	item := testSession(repo, "d1", "Planner")
	item.Role, item.PlansRoot = "d", plans
	for _, path := range []string{"../escape.md", ".agentb/policy.json"} {
		if _, err := write.Call(context.Background(), item, map[string]any{"path": path, "content": "no"}); err == nil {
			t.Fatalf("unbound d accepted %s", path)
		}
		if item.PlanID != "" || item.PlanDir != "" {
			t.Fatalf("refused write bound a plan: %+v", item.Snapshot())
		}
	}
	if _, err := write.Call(context.Background(), item, map[string]any{"path": "draft.md", "content": "plan A"}); err != nil {
		t.Fatal(err)
	}
	planA := item.PlanDir
	if item.PlanID == "" || planA == "" {
		t.Fatalf("plan was not bound: %+v", item.Snapshot())
	}
	for _, path := range []string{"plan.md", "NOTES.md", filepath.Join("plan", "items")} {
		if _, err := os.Stat(filepath.Join(planA, path)); err != nil {
			t.Fatalf("layout %s: %v", path, err)
		}
	}
	if got, err := read.Call(context.Background(), item, map[string]any{"path": filepath.Join(repo, "source.txt")}); err != nil || !strings.Contains(got, "repository") {
		t.Fatalf("repo read=%q err=%v", got, err)
	}
	if got, err := read.Call(context.Background(), item, map[string]any{"path": "draft.md"}); err != nil || !strings.Contains(got, "plan A") {
		t.Fatalf("plan read=%q err=%v", got, err)
	}
	if _, err := write.Call(context.Background(), item, map[string]any{"path": filepath.Join(repo, "blocked.txt"), "content": "no"}); err == nil {
		t.Fatal("d write escaped into repository")
	}
	if _, err := write.Call(withOSPathPolicy(context.Background()), item, map[string]any{"path": filepath.Join(repo, "operator-blocked.txt"), "content": "no"}); err == nil {
		t.Fatal("operator identity widened the d plan jail")
	}
	planB := filepath.Join(plans, "other")
	if err := os.MkdirAll(planB, 0o755); err != nil {
		t.Fatal(err)
	}
	item.Workspace = root
	if _, err := read.Call(context.Background(), item, map[string]any{"path": filepath.Join(planB, "plan.md")}); err == nil {
		t.Fatal("d read reached another plan through an ancestor workspace")
	}
	if _, err := edit.Call(context.Background(), item, map[string]any{"path": filepath.Join(planB, "plan.md"), "old_string": "x", "new_string": "y"}); err == nil {
		t.Fatal("d edit reached another plan")
	}
	if _, err := os.Stat(filepath.Join(repo, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("escaped file exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "operator-blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("operator escaped file exists: %v", err)
	}
}

func TestPlanPageModelWritesRefuseUntilAcceptGate(t *testing.T) {
	root := t.TempDir()
	plan := filepath.Join(root, "plans", "one")
	if err := os.MkdirAll(plan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plan, "plan.md"), []byte("[ ] old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	edit, write, _, _ := testTools(root)
	item := testSession(root, "d-plan", "Planner")
	item.Role, item.PlansRoot, item.PlanDir = "d", filepath.Dir(plan), plan
	item.SetPlanPage(true)
	if _, err := edit.Call(context.Background(), item, map[string]any{"path": "plan.md", "old_string": "old", "new_string": "new"}); err == nil || !strings.Contains(err.Error(), "accepting a proposal") {
		t.Fatalf("direct edit err=%v", err)
	}
	if _, err := write.Call(context.Background(), item, map[string]any{"path": "other.md", "content": "no"}); err == nil || !strings.Contains(err.Error(), "accepting a proposal") {
		t.Fatalf("direct write err=%v", err)
	}
	if !item.BeginPlanAccept() {
		t.Fatal("accept gate did not open")
	}
	if _, err := edit.Call(context.Background(), item, map[string]any{"path": "plan.md", "old_string": "old", "new_string": "new"}); err != nil {
		t.Fatal(err)
	}
	item.EndPlanAccept()
	if got, err := os.ReadFile(filepath.Join(plan, "plan.md")); err != nil || string(got) != "[ ] new\n" {
		t.Fatalf("plan=%q err=%v", got, err)
	}
}

func TestModelFileToolsRefuseRepoPolicyDirectoryByNamedRule(t *testing.T) {
	root := t.TempDir()
	edit, write, _, _ := testTools(root)
	item := testSession(root, "policy", "Policy")
	if result, err := write.Call(context.Background(), item, map[string]any{"path": ".agentb/policy.json", "content": "{}"}); err == nil || !strings.Contains(err.Error(), "repo-policy immutability rule") || result != "" {
		t.Fatalf("write result=%q err=%v", result, err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".agentb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".agentb", "policy.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := edit.Call(context.Background(), item, map[string]any{"path": ".agentb/policy.json", "old_string": "{}", "new_string": "{\"version\":1}"}); err == nil || !strings.Contains(err.Error(), "repo-policy immutability rule") || result != "" {
		t.Fatalf("edit result=%q err=%v", result, err)
	}
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

func TestEditFileSchemaRemainsCompatible(t *testing.T) {
	schema := (&EditFile{}).Schema()
	properties := schema["properties"].(map[string]any)
	for _, name := range []string{"path", "old_string", "new_string"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("schema missing %s: %#v", name, schema)
		}
	}
	required := schema["required"].([]string)
	if strings.Join(required, ",") != "path,old_string,new_string" {
		t.Fatalf("required=%v", required)
	}
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
		if err != nil || !strings.Contains(got, "strategy: whitespace-normalized") {
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
		if err != nil || !strings.HasPrefix(got, "ok: deleted lines") || !strings.Contains(got, "@@ -2,1 +2,0 @@\n-b") || strings.Contains(got, "-c") {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("near_miss_at_least_0_6", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("one\nexpected old line\nchanged file line\n"))
		_, err := edit(t, tool, s, path, "one\nexpected old line\nthree", "REPLACEMENT")
		if err == nil || !strings.Contains(err.Error(), "Closest match: lines") || !strings.Contains(err.Error(), `"changed file line"`) || !strings.Contains(err.Error(), `"three"`) {
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
	t.Run("smart_quotes_and_dashes_match_ASCII", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("message = \"don't re-run - wait\"\n"))
		got, err := edit(t, tool, s, path, "message = “don’t re—run – wait”", "message = \"done\"")
		if err != nil || !strings.Contains(got, "strategy: whitespace-normalized") {
			t.Fatalf("got %q, %v", got, err)
		}
		data, _ := os.ReadFile(path)
		if string(data) != "message = \"done\"\n" {
			t.Fatalf("file %q", data)
		}
	})
	t.Run("block_anchor_accepts_loose_middle", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("before\nBEGIN unique\nactual middle one\nactual middle two\nEND unique\nafter\n"))
		got, err := edit(t, tool, s, path, "BEGIN unique\nexpected middle\nstill expected\nEND unique", "BEGIN unique\nnew middle\nEND unique")
		if err != nil || !strings.Contains(got, "strategy: block-anchor; loose middle accepted") {
			t.Fatalf("got %q, %v", got, err)
		}
		data, _ := os.ReadFile(path)
		if string(data) != "before\nBEGIN unique\nnew middle\nEND unique\nafter\n" {
			t.Fatalf("file %q", data)
		}
	})
	t.Run("result_is_unified_diff", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.txt", []byte("one\ntwo\nthree\n"))
		got, err := edit(t, tool, s, path, "two", "TWO")
		if err != nil || !strings.Contains(got, "--- a/x.txt\n+++ b/x.txt\n@@ -2,1 +2,1 @@\n-two\n+TWO") {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("JSON_syntax_diagnostic_describes_failure", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.json", []byte("{\"ok\": true}\n"))
		got, err := edit(t, tool, s, path, "true", "}")
		if err != nil || !strings.Contains(got, "syntax check: failed (json):") || strings.Contains(got, "json.Valid") {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("Go_syntax_diagnostic_describes_failure", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		path := writeFixture(t, root, "x.go", []byte("package sample\n\nfunc ok() {}\n"))
		got, err := edit(t, tool, s, path, "func ok() {}", "func broken( {")
		if err != nil || !strings.Contains(got, "syntax check: failed (go):") || strings.Contains(got, "parser.ParseFile") {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("outside_workspace", func(t *testing.T) {
		root := t.TempDir()
		tool, _, _, _ := testTools(root)
		s := testSession(root, "a", "A")
		for _, path := range []string{"../x.go", filepath.Join(filepath.Dir(root), "other.go")} {
			_, err := edit(t, tool, s, path, "a", "b")
			if err == nil || !strings.Contains(err.Error(), "path is outside the folder") {
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

func TestWriteFileByteIdenticalContentIsUnchanged(t *testing.T) {
	root := t.TempDir()
	_, tool, _, workspaces := testTools(root)
	item := testSession(root, "a", "A")
	path := writeFixture(t, root, "same.txt", []byte("same bytes\n"))
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	result, err := tool.Call(context.Background(), item, map[string]any{"path": path, "content": "same bytes\n"})
	if err != nil || !strings.HasPrefix(result, "unchanged:") {
		t.Fatalf("result=%q err=%v", result, err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("mtime changed: before=%s after=%s", before.ModTime(), after.ModTime())
	}
	if _, ok := workspaces.LastWriter(root, "same.txt"); ok {
		t.Fatal("unchanged write was recorded as a mutation")
	}
}
