//go:build windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/credential"
	"harness/internal/session"
)

// TestStep4LiveThreeRootShell is operator-gated because it needs a disposable
// local account and machine ACLs prepared by the Step 4 verification harness.
func TestStep4LiveThreeRootShell(t *testing.T) {
	account := os.Getenv("AGENTB_STEP4_LIVE_ACCOUNT")
	applicationRoot := os.Getenv("AGENTB_STEP4_LIVE_APPLICATION_ROOT")
	dataRoot := os.Getenv("AGENTB_STEP4_LIVE_DATA_ROOT")
	workspaceRoot := os.Getenv("AGENTB_STEP4_LIVE_WORKSPACE_ROOT")
	if account == "" || applicationRoot == "" || dataRoot == "" || workspaceRoot == "" {
		t.Skip("set all AGENTB_STEP4_LIVE_* variables for the operator test")
	}

	store := credential.New(dataRoot)
	password, err := store.Read()
	if err != nil {
		t.Fatalf("operator could not decrypt relocated credential: %v", err)
	}
	clearBytes(password)

	cfg := config.Defaults(workspaceRoot)
	fileRoutingGuard := false
	cfg.Shell.FileRoutingGuard = &fileRoutingGuard
	cfg.Shell.ServiceAccount = config.ShellServiceAccount{Enabled: true, Account: account, Domain: "."}
	shell := NewShell(cfg.Shell)
	shell.Configure(cfg)
	shell.SetCredentialStore(store)
	s := &session.Session{ID: "step4-live", Workspace: workspaceRoot}

	workspaceMarker := filepath.Join(workspaceRoot, "service-write.txt")
	detail := shell.CallDetailed(context.Background(), s, map[string]any{
		"command": "$self = Get-CimInstance Win32_Process -Filter (\"ProcessId=$PID\"); " +
			"Write-Output (\"child_pid=$PID\"); Write-Output (\"parent_pid=\" + $self.ParentProcessId); " +
			"Write-Output (\"child_name=\" + $self.Name); whoami; Set-Content -LiteralPath " +
			quotePowerShell(workspaceMarker) + " -Value verified -ErrorAction Stop; (Get-Location).Path",
	})
	if detail.Err != nil || detail.OperatorOverrideReason != "" {
		t.Fatalf("service shell in relocated workspace failed: %+v", detail)
	}
	if !strings.Contains(strings.ToLower(detail.Content), "\\"+strings.ToLower(account)) || !strings.Contains(detail.Content, workspaceRoot) {
		t.Fatalf("service shell did not report expected identity/workspace: %q", detail.Content)
	}
	if !strings.Contains(detail.Content, "parent_pid="+strconv.Itoa(os.Getpid())) || !strings.Contains(strings.ToLower(detail.Content), "child_name=powershell.exe") {
		t.Fatalf("service shell was not a direct PowerShell child of the harness process: %q", detail.Content)
	}
	t.Log(strings.TrimSpace(detail.Content))
	if body, err := os.ReadFile(workspaceMarker); err != nil || strings.TrimSpace(string(body)) != "verified" {
		t.Fatalf("workspace marker = %q, %v", body, err)
	}

	for _, denied := range []struct {
		name string
		path string
	}{
		{"application", filepath.Join(applicationRoot, "service-must-not-write.txt")},
		{"data", filepath.Join(dataRoot, "service-must-not-write.txt")},
	} {
		t.Run(denied.name+"_write_denied", func(t *testing.T) {
			detail := shell.CallDetailed(context.Background(), s, map[string]any{
				"command": "Set-Content -LiteralPath " + quotePowerShell(denied.path) + " -Value forbidden -ErrorAction Stop",
			})
			if detail.Err == nil && detail.OperatorOverrideReason == "" {
				t.Fatalf("service write unexpectedly succeeded: %+v", detail)
			}
			if _, err := os.Stat(denied.path); !os.IsNotExist(err) {
				t.Fatalf("service account created denied file: %v", err)
			}
		})
	}

	t.Run("data_read_denied", func(t *testing.T) {
		detail := shell.CallDetailed(context.Background(), s, map[string]any{
			"command": "Get-Content -LiteralPath " + quotePowerShell(filepath.Join(dataRoot, "harness.json")) + " -ErrorAction Stop",
		})
		if detail.Err == nil && detail.OperatorOverrideReason == "" {
			t.Fatalf("service read of operator config unexpectedly succeeded: %+v", detail)
		}
	})
}

func quotePowerShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
