package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/session"
)

func TestWindowUTF8UsesByteOffsetsWithoutSplittingRunes(t *testing.T) {
	text := "ab🙂cd"
	first, err := windowUTF8(text, 1, 5)
	if err != nil || first.Content != "ab" || first.Bytes != 2 || first.TotalBytes != 8 || !first.More || first.NextOffset != 3 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := windowUTF8(text, first.NextOffset, 5)
	if err != nil || second.Content != "🙂c" || second.Bytes != 5 || !second.More || second.NextOffset != 8 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	last, err := windowUTF8(text, second.NextOffset, 5)
	if err != nil || last.Content != "d" || last.Bytes != 1 || last.More || last.NextOffset != 0 {
		t.Fatalf("last=%+v err=%v", last, err)
	}
	if _, err := windowUTF8(text, 4, 5); err == nil || !strings.Contains(err.Error(), "inside a multi-byte") {
		t.Fatalf("mid-rune offset error=%v", err)
	}
}

func TestReadFileUsesSharedByteWindow(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "single-line.txt")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFile(config.ReadFileTool{DefaultLimit: 4, MaxLimit: 4})
	item := &session.Session{Workspace: root, LastSeen: map[string]time.Time{}}
	first, err := tool.Call(context.Background(), item, map[string]any{"path": path})
	if err != nil || first != "[byte window: offset=1 bytes=4 total=10 more=true next_offset=5 start_line=1 start_mid_line=false end_mid_line=true]\n1: 0123" {
		t.Fatalf("first=%q err=%v", first, err)
	}
	second, err := tool.Call(context.Background(), item, map[string]any{"path": path, "offset": 5})
	if err != nil || second != "[byte window: offset=5 bytes=4 total=10 more=true next_offset=9 start_line=1 start_mid_line=true end_mid_line=true]\n1: 4567" {
		t.Fatalf("second=%q err=%v", second, err)
	}
}

func TestReadFileNumbersLinesAndMarksMidLineBoundaries(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.go")
	if err := os.WriteFile(path, []byte("alpha\nbravo\ncharlie\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFile(config.ReadFileTool{DefaultLimit: 8, MaxLimit: 8})
	item := &session.Session{Workspace: root, LastSeen: map[string]time.Time{}}

	first, err := tool.Call(context.Background(), item, map[string]any{"path": path})
	wantFirst := "[byte window: offset=1 bytes=8 total=20 more=true next_offset=9 start_line=1 start_mid_line=false end_mid_line=true]\n1: alpha\n2: br"
	if err != nil || first != wantFirst {
		t.Fatalf("first=%q err=%v", first, err)
	}
	second, err := tool.Call(context.Background(), item, map[string]any{"path": path, "offset": 9})
	wantSecond := "[byte window: offset=9 bytes=8 total=20 more=true next_offset=17 start_line=2 start_mid_line=true end_mid_line=true]\n2: avo\n3: char"
	if err != nil || second != wantSecond {
		t.Fatalf("second=%q err=%v", second, err)
	}
	last, err := tool.Call(context.Background(), item, map[string]any{"path": path, "offset": 17})
	wantLast := "[byte window: offset=17 bytes=4 total=20 more=false start_line=3 start_mid_line=true end_mid_line=false]\n3: lie\n"
	if err != nil || last != wantLast {
		t.Fatalf("last=%q err=%v", last, err)
	}
}

func TestReadFileDefaultWindowNumbersLongSource(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "generated.go")
	var source strings.Builder
	for line := 1; line <= 200; line++ {
		fmt.Fprintf(&source, "source line %03d\n", line)
	}
	if err := os.WriteFile(path, []byte(source.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults(root).Tools.ReadFile
	result, err := NewReadFile(cfg).Call(context.Background(), &session.Session{Workspace: root, LastSeen: map[string]time.Time{}}, map[string]any{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result, "[byte window: offset=1 bytes=3200 total=3200 more=false start_line=1 start_mid_line=false end_mid_line=false]\n1: source line 001\n") || !strings.HasSuffix(result, "200: source line 200\n") {
		t.Fatalf("unexpected numbered source window: %q", result)
	}
}

func TestModelVisibleByteWindowDescriptionsNameCursorParameter(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	registry := New(NewReadFile(cfg.Tools.ReadFile), NewFetch(cfg.Tools.Fetch))
	schemas := registry.Schemas(map[string]bool{"read_file": true, "fetch_url": true})
	if len(schemas) != 2 {
		t.Fatalf("schema count=%d", len(schemas))
	}
	for _, raw := range schemas {
		function := raw.(map[string]any)["function"].(map[string]any)
		description := function["description"].(string)
		if !strings.Contains(description, "pass returned next_offset as offset to advance") {
			t.Errorf("%s description does not explain cursor reuse: %q", function["name"], description)
		}
	}
}
