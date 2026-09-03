//go:build windows

package hardening

import (
	"context"
	"os"
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

func TestSystemPowerShellModulesComeBeforePowerShell7Modules(t *testing.T) {
	powershell := `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
	environment := systemPowerShellEnvironment([]string{
		"PATH=C:\\Windows",
		`PSModulePath=C:\Program Files\PowerShell\7\Modules;C:\Windows\System32\WindowsPowerShell\v1.0\Modules`,
	}, powershell)
	wanted := filepath.Join(filepath.Dir(powershell), "Modules")
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, "PSModulePath") {
			paths := filepath.SplitList(value)
			if len(paths) == 0 || !strings.EqualFold(paths[0], wanted) {
				t.Fatalf("PSModulePath = %q, want %q first", value, wanted)
			}
			if strings.Count(strings.ToLower(value), strings.ToLower(wanted)) != 1 {
				t.Fatalf("PSModulePath duplicates system modules: %q", value)
			}
			return
		}
	}
	t.Fatal("PSModulePath was not set")
}

func TestSystemPowerShellModulesAreAddedWhenMissing(t *testing.T) {
	powershell := `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
	environment := systemPowerShellEnvironment([]string{"PATH=" + os.Getenv("PATH")}, powershell)
	for _, entry := range environment {
		if strings.HasPrefix(strings.ToLower(entry), "psmodulepath=") {
			return
		}
	}
	t.Fatal("PSModulePath was not added")
}

func TestElevatedHardeningCommandUsesUAC(t *testing.T) {
	command := elevatedCommand(`C:\Windows\powershell.exe`, []string{"-File", `C:\Program Files\AgentB\apply-hardening.ps1`, "-Mode", "Apply"})
	for _, wanted := range []string{"Start-Process", "-Verb RunAs", "-WindowStyle Hidden", "WaitForExit", `"C:\Program Files\AgentB\apply-hardening.ps1"`} {
		if !strings.Contains(command, wanted) {
			t.Fatalf("elevation command does not contain %q:\n%s", wanted, command)
		}
	}
}
