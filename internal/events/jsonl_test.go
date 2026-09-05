package events

import (
	"os"
	"testing"
)

func TestCloseSessionReleasesWriter(t *testing.T) {
	writers, err := NewWriters(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writers.Close() })
	path, err := writers.OpenSession("main")
	if err != nil {
		t.Fatal(err)
	}
	if err := writers.Write(New(ToolResult, "main", "run", map[string]any{"ok": true})); err != nil {
		t.Fatal(err)
	}
	if err := writers.CloseSession("main"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("session log remained open: %v", err)
	}
}

func TestWriteReturnsMarshalFailure(t *testing.T) {
	writers, err := NewWriters(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writers.Close() })
	if err := writers.Write(New(Error, "", "", make(chan int))); err == nil {
		t.Fatal("unsupported event data was reported as persisted")
	}
}
