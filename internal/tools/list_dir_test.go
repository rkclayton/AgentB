package tools

import (
	"context"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/session"
)

func TestListDirReportsEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	tool := NewListDir(config.Defaults(root).Tools.ListDir)
	result, err := tool.Call(context.Background(), &session.Session{Workspace: root, LastSeen: map[string]time.Time{}}, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if result != "directory is empty" {
		t.Fatalf("result=%q", result)
	}
}
