//go:build windows

package hardening

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusReportsAbsentAccountWithoutMutation(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	manager := New(
		filepath.Join(root, "scripts", "apply-acls.ps1"),
		filepath.Join(root, "scripts", "apply-firewall-rule.ps1"),
		filepath.Join(root, "scripts", "apply-hardening.ps1"),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	status, err := manager.Status(ctx, Request{AccountName: "agentb-test-account-that-does-not-exist", HarnessDirectory: root, WorkspaceDirectory: filepath.Join(root, "workspace"), ModelAddress: "127.0.0.1", ModelPort: 8080})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Supported || status.Applied || status.ACL.AccountExists || status.Firewall.AccountExists {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestElevatedHardeningCommandUsesUAC(t *testing.T) {
	command := elevatedCommand(`C:\Windows\powershell.exe`, []string{"-File", `C:\Program Files\AgentB\apply-hardening.ps1`, "-Mode", "Apply"})
	for _, wanted := range []string{"Start-Process", "-Verb RunAs", "-WindowStyle Hidden", "WaitForExit", `"C:\Program Files\AgentB\apply-hardening.ps1"`} {
		if !strings.Contains(command, wanted) {
			t.Fatalf("elevation command does not contain %q:\n%s", wanted, command)
		}
	}
}
