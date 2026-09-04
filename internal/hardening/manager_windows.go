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
	"syscall"
	"unicode/utf16"
)

const (
	aclStatusMarker      = "AGENTB_ACL_STATUS="
	firewallStatusMarker = "AGENTB_FIREWALL_STATUS="
)

var procIsUserAnAdmin = syscall.NewLazyDLL("shell32.dll").NewProc("IsUserAnAdmin")

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
		Supported: true, HarnessElevated: isUserAnAdmin(), ModelAddress: request.ModelAddress, ModelPort: request.ModelPort,
		ACL: acl, Firewall: firewall, Applied: acl.Applied && firewall.Applied,
	}, nil
}

func isUserAnAdmin() bool {
	result, _, _ := procIsUserAnAdmin.Call()
	return result != 0
}

func inspectComponent(ctx context.Context, powershell, script, marker string, arguments []string) (ComponentStatus, error) {
	absolute, err := filepath.Abs(script)
	if err != nil {
		return ComponentStatus{}, err
	}
	args := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", absolute}
	args = append(args, arguments...)
	command := exec.CommandContext(ctx, powershell, args...)
	command.Env = systemPowerShellEnvironment(os.Environ(), powershell)
	output, runErr := command.CombinedOutput()
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
	// Status performs the same read-only policy inspection and returns structured
	// component summaries. Avoid wrapping expected drift in PowerShell stack text.
	if action == "verify" {
		return RunResult{Attempted: true}, nil
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
	resultFile, err := os.CreateTemp("", "agentb-hardening-result-*.txt")
	if err != nil {
		return RunResult{}, fmt.Errorf("create hardening result channel: %w", err)
	}
	resultPath := resultFile.Name()
	if err := resultFile.Close(); err != nil {
		os.Remove(resultPath)
		return RunResult{}, fmt.Errorf("prepare hardening result channel: %w", err)
	}
	defer os.Remove(resultPath)
	arguments = append(arguments, "-ResultPath", resultPath)
	command := elevatedCommand(m.powershell, arguments)
	launcher := exec.CommandContext(ctx, m.powershell,
		"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encode(command),
	)
	launcher.Env = systemPowerShellEnvironment(os.Environ(), m.powershell)
	output, runErr := launcher.CombinedOutput()
	attempted := strings.Contains(string(output), "AGENTB_ELEVATED_STARTED")
	if runErr != nil {
		if strings.Contains(string(output), "AGENTB_ELEVATION_NOT_STARTED") {
			return RunResult{}, fmt.Errorf("Windows elevation was canceled or could not be started")
		}
		if detail := hardeningResult(resultPath); detail != "" {
			return RunResult{Attempted: attempted}, fmt.Errorf("elevated hardening failed: %s", detail)
		}
		return RunResult{Attempted: attempted}, fmt.Errorf("elevated hardening failed: %s", safeError(output, runErr))
	}
	if !attempted {
		return RunResult{}, fmt.Errorf("elevated hardening did not start")
	}
	return RunResult{Attempted: true}, nil
}

func hardeningResult(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	const limit = 8 << 10
	if len(data) > limit {
		data = data[len(data)-limit:]
	}
	return strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
}

// Windows PowerShell can inherit a PSModulePath headed by PowerShell 7's
// incompatible built-in modules. Put its own module directory first so Get-Acl,
// LocalAccounts, and NetSecurity autoload consistently from the web process.
func systemPowerShellEnvironment(environment []string, powershell string) []string {
	systemModules := filepath.Clean(filepath.Join(filepath.Dir(powershell), "Modules"))
	result := append([]string(nil), environment...)
	found := false
	for index, entry := range result {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !strings.EqualFold(key, "PSModulePath") {
			continue
		}
		found = true
		paths := []string{systemModules}
		for _, candidate := range filepath.SplitList(value) {
			if candidate != "" && !strings.EqualFold(filepath.Clean(candidate), systemModules) {
				paths = append(paths, candidate)
			}
		}
		result[index] = key + "=" + strings.Join(paths, string(os.PathListSeparator))
	}
	if !found {
		result = append(result, "PSModulePath="+systemModules)
	}
	return result
}

func elevatedCommand(executable string, arguments []string) string {
	quoted := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		quoted = append(quoted, `"`+strings.ReplaceAll(argument, `"`, `\"`)+`"`)
	}
	return fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
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
	if strings.Contains(text, "#< CLIXML") {
		// Progress records from a hidden Windows PowerShell process serialize as
		// CLIXML and are not useful operator-facing diagnostics.
		return err.Error()
	}
	if text == "" {
		return err.Error()
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return strings.Join(lines, " ")
}
