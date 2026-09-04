package tools

import (
	"context"
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
	if err != nil || first != "[byte window: offset=1 bytes=4 total=10 more=true next_offset=5]\n0123" {
		t.Fatalf("first=%q err=%v", first, err)
	}
	second, err := tool.Call(context.Background(), item, map[string]any{"path": path, "offset": 5})
	if err != nil || second != "[byte window: offset=5 bytes=4 total=10 more=true next_offset=9]\n4567" {
		t.Fatalf("second=%q err=%v", second, err)
	}
}
