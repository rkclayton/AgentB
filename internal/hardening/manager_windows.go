//go:build windows

package hardening

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

const (
	aclStatusMarker      = "AGENTB_ACL_STATUS="
	firewallStatusMarker = "AGENTB_FIREWALL_STATUS="
)

type windowsManager struct {
	aclScript, firewallScript, orchestrationScript string
	powershell                                     string
}

func New(aclScript, firewallScript, orchestrationScript string) Manager {
	powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(powershell); err != nil {
		powershell = "powershell.exe"
	}
	return &windowsManager{aclScript: aclScript, firewallScript: firewallScript, orchestrationScript: orchestrationScript, powershell: powershell}
}

func (m *windowsManager) Status(ctx context.Context, request Request) (Status, error) {
	acl, err := inspectComponent(ctx, m.powershell, m.aclScript, aclStatusMarker, []string{
		"-AccountName", request.AccountName,
		"-HarnessDirectory", request.HarnessDirectory,
		"-WorkspaceDirectory", request.WorkspaceDirectory,
		"-Inspect",
	})
	if err != nil {
		return Status{}, fmt.Errorf("inspect ACL policy: %w", err)
	}
	firewall, err := inspectComponent(ctx, m.powershell, m.firewallScript, firewallStatusMarker, []string{
		"-AccountName", request.AccountName,
		"-ModelAddress", request.ModelAddress,
		"-ModelPort", fmt.Sprint(request.ModelPort),
		"-Inspect",
	})
	if err != nil {
		return Status{}, fmt.Errorf("inspect firewall policy: %w", err)
	}
	return Status{
		Supported: true, ModelAddress: request.ModelAddress, ModelPort: request.ModelPort,
		ACL: acl, Firewall: firewall, Applied: acl.Applied && firewall.Applied,
	}, nil
}

func inspectComponent(ctx context.Context, powershell, script, marker string, arguments []string) (ComponentStatus, error) {
	absolute, err := filepath.Abs(script)
	if err != nil {
		return ComponentStatus{}, err
	}
	args := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", absolute}
	args = append(args, arguments...)
	output, runErr := exec.CommandContext(ctx, powershell, args...).CombinedOutput()
	if runErr != nil {
		return ComponentStatus{}, fmt.Errorf("%s", safeError(output, runErr))
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, marker) {
			continue
		}
		var status ComponentStatus
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, marker)), &status); err != nil {
			return ComponentStatus{}, fmt.Errorf("decode status: %w", err)
		}
		return status, nil
	}
	return ComponentStatus{}, fmt.Errorf("status marker was not returned")
}

func (m *windowsManager) Run(ctx context.Context, action string, request Request) (RunResult, error) {
	mode := map[string]string{"apply": "Apply", "verify": "Verify", "remove": "Remove"}[action]
	if mode == "" {
		return RunResult{}, fmt.Errorf("hardening action must be apply, verify, or remove")
	}
	script, err := filepath.Abs(m.orchestrationScript)
	if err != nil {
		return RunResult{}, fmt.Errorf("resolve hardening script: %w", err)
	}
	arguments := []string{
		"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", script,
		"-Mode", mode,
		"-AccountName", request.AccountName,
		"-HarnessDirectory", request.HarnessDirectory,
		"-WorkspaceDirectory", request.WorkspaceDirectory,
		"-ModelAddress", request.ModelAddress,
		"-ModelPort", fmt.Sprint(request.ModelPort),
	}
	if action == "verify" {
		output, runErr := exec.CommandContext(ctx, m.powershell, arguments...).CombinedOutput()
		if runErr != nil {
			return RunResult{Attempted: true}, fmt.Errorf("hardening verification failed: %s", safeError(output, runErr))
		}
		return RunResult{Attempted: true}, nil
	}
	command := elevatedCommand(m.powershell, arguments)
	output, runErr := exec.CommandContext(ctx, m.powershell,
		"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encode(command),
	).CombinedOutput()
	attempted := strings.Contains(string(output), "AGENTB_ELEVATED_STARTED")
	if runErr != nil {
		if strings.Contains(string(output), "AGENTB_ELEVATION_NOT_STARTED") {
			return RunResult{}, fmt.Errorf("Windows elevation was canceled or could not be started")
		}
		return RunResult{Attempted: attempted}, fmt.Errorf("elevated hardening failed: %s", safeError(output, runErr))
	}
	if !attempted {
		return RunResult{}, fmt.Errorf("elevated hardening did not start")
	}
	return RunResult{Attempted: true}, nil
}

func elevatedCommand(executable string, arguments []string) string {
	quoted := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		quoted = append(quoted, `"`+strings.ReplaceAll(argument, `"`, `\"`)+`"`)
	}
	return fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
try {
  $process = Start-Process -FilePath '%s' -ArgumentList '%s' -Verb RunAs -WindowStyle Hidden -PassThru
  Write-Output 'AGENTB_ELEVATED_STARTED'
  $process.WaitForExit()
  if ($process.ExitCode -ne 0) { exit $process.ExitCode }
} catch {
  Write-Output 'AGENTB_ELEVATION_NOT_STARTED'
  exit 1
}
`, strings.ReplaceAll(executable, "'", "''"), strings.ReplaceAll(strings.Join(quoted, " "), "'", "''"))
}

func encode(command string) string {
	encoded := utf16.Encode([]rune(command))
	data := make([]byte, len(encoded)*2)
	for index, value := range encoded {
		data[index*2], data[index*2+1] = byte(value), byte(value>>8)
	}
	return base64.StdEncoding.EncodeToString(data)
}

func safeError(output []byte, err error) string {
	text := strings.TrimSpace(strings.ReplaceAll(string(output), "\r\n", "\n"))
	if text == "" {
		return err.Error()
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return strings.Join(lines, " ")
}
