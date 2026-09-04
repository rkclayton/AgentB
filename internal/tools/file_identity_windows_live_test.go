//go:build windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/credential"
	"harness/internal/events"
	"harness/internal/session"
)

// This operator-gated test exercises LogonUserW and impersonation without
// putting a password in an argument, environment variable, fixture, or log.
func TestLiveServiceFileIdentity(t *testing.T) {
	configPath := os.Getenv("AGENTB_LIVE_CONFIG")
	targetPath := os.Getenv("AGENTB_LIVE_FILE_PATH")
	if configPath == "" || targetPath == "" {
		t.Skip("set AGENTB_LIVE_CONFIG and AGENTB_LIVE_FILE_PATH for the operator test")
	}
	cfg, _, _, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Shell.ServiceAccount.Enabled {
		t.Fatal("service identity is disabled")
	}
	identity := NewFileIdentity(credential.New(configPath))
	identity.Configure(*cfg)
	tool := identity.Wrap(NewListDir(cfg.Tools.ListDir))
	s := &session.Session{Workspace: cfg.Workspace, LastSeen: map[string]time.Time{}}
	result, err := tool.Call(context.Background(), s, map[string]any{"path": targetPath, "depth": 1})
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Fatal("service-account directory read returned no entries")
	}

	deniedPath := filepath.Join(filepath.Dir(configPath), ".agentb-file-identity-deny-test")
	defer os.Remove(deniedPath)
	coordinator := NewFileCoordinator(session.NewWorkspaceRegistry(), func(string) string { return "test" }, events.NewBus())
	writer := identity.Wrap(NewWriteFile(coordinator)).(DetailedTool)
	detail := writer.CallDetailed(context.Background(), s, map[string]any{"path": deniedPath, "content": "permission probe"})
	if !strings.Contains(detail.OperatorOverrideReason, "denied permission") {
		t.Fatalf("control-tree write did not produce an OS permission override: %+v", detail)
	}
	if _, err := os.Stat(deniedPath); !os.IsNotExist(err) {
		t.Fatalf("service account unexpectedly created control-tree file: %v", err)
	}
}
