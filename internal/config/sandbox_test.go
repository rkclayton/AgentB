package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxWorkspaceDeclarationIsExactAndDeterministic(t *testing.T) {
	workspace := t.TempDir()
	cfg := Defaults(workspace)
	cfg.Sandbox.Workspaces[workspace] = true
	first, ok := cfg.SandboxForWorkspace(workspace)
	second, again := cfg.SandboxForWorkspace(filepath.Clean(workspace))
	if !ok || !again || first != second || !strings.HasPrefix(first, "agentb-") {
		t.Fatalf("targets=%q/%q ok=%t/%t", first, second, ok, again)
	}
	if _, ok := cfg.SandboxForWorkspace(t.TempDir()); ok {
		t.Fatal("undeclared workspace received a sandbox target")
	}
	cfg.Sandbox.Workspaces["relative"] = true
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("validation=%v", err)
	}
}
