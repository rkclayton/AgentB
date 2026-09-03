//go:build windows

package serviceaccount

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

const statusMarker = "AGENTB_ACCOUNT_STATUS="

type windowsManager struct {
	scriptPath string
	powershell string
}

func New(scriptPath string) Manager {
	powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(powershell); err != nil {
		powershell = "powershell.exe"
	}
	return &windowsManager{scriptPath: scriptPath, powershell: powershell}
}

func (m *windowsManager) Status(ctx context.Context, account string) (Status, error) {
	output, err := exec.CommandContext(ctx, m.powershell,
		"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", m.scriptPath, "-Inspect", "-AccountName", account,
	).CombinedOutput()
	if err != nil {
		return Status{}, fmt.Errorf("inspect local service account: %s", safePowerShellError(output, err))
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, statusMarker) {
			continue
		}
		var status Status
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, statusMarker)), &status); err != nil {
			return Status{}, fmt.Errorf("decode local service-account status: %w", err)
		}
		return status, nil
	}
	return Status{}, fmt.Errorf("inspect local service account: status was not returned")
}

func (m *windowsManager) Setup(ctx context.Context, account, credentialPath string, reset bool) (SetupResult, error) {
	scriptPath, err := filepath.Abs(m.scriptPath)
	if err != nil {
		return SetupResult{}, fmt.Errorf("resolve service-account setup script: %w", err)
	}
	credentialPath, err = filepath.Abs(credentialPath)
	if err != nil {
		return SetupResult{}, fmt.Errorf("resolve service-account credential store: %w", err)
	}
	if _, err := os.Stat(scriptPath); err != nil {
		return SetupResult{}, fmt.Errorf("service-account setup script is unavailable: %w", err)
	}
	if _, err := os.Stat(credentialPath); err != nil {
		return SetupResult{}, fmt.Errorf("service-account credential store is unavailable: %w", err)
	}
	arguments := []string{
		"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", scriptPath, "-AccountName", account, "-CredentialStore", credentialPath,
		"-Confirm:$false",
	}
	if reset {
		arguments = append(arguments, "-ResetPassword")
	}
	command := elevatedCommand(m.powershell, arguments)
	output, runErr := exec.CommandContext(ctx, m.powershell,
		"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(command),
	).CombinedOutput()
	attempted := strings.Contains(string(output), "AGENTB_ELEVATED_STARTED")
	if runErr != nil {
		if strings.Contains(string(output), "AGENTB_ELEVATION_NOT_STARTED") {
			return SetupResult{}, fmt.Errorf("Windows elevation was canceled or could not be started")
		}
		return SetupResult{Attempted: attempted}, fmt.Errorf("elevated service-account setup failed: %s", safePowerShellError(output, runErr))
	}
	if !attempted {
		return SetupResult{}, fmt.Errorf("elevated service-account setup did not start")
	}
	return SetupResult{Attempted: true}, nil
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

func encodePowerShell(command string) string {
	encoded := utf16.Encode([]rune(command))
	bytes := make([]byte, len(encoded)*2)
	for index, value := range encoded {
		bytes[index*2] = byte(value)
		bytes[index*2+1] = byte(value >> 8)
	}
	return base64.StdEncoding.EncodeToString(bytes)
}

func safePowerShellError(output []byte, err error) string {
	text := strings.TrimSpace(strings.ReplaceAll(string(output), "\r\n", "\n"))
	if text == "" {
		return err.Error()
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	return strings.Join(lines, " ")
}
