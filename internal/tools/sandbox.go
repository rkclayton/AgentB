package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"harness/internal/config"
	"harness/internal/session"
)

var (
	sandboxProbeMu    sync.Mutex
	sandboxProbeCache = map[string]SandboxStatus{}
	hypervisorOnce    sync.Once
	hypervisorState   string
)

type SandboxStatus struct {
	Available  bool     `json:"available"`
	Installed  bool     `json:"installed"`
	SignedIn   bool     `json:"signed_in"`
	Executable string   `json:"executable,omitempty"`
	Version    string   `json:"version,omitempty"`
	Hypervisor string   `json:"hypervisor"`
	Reason     string   `json:"reason"`
	Findings   []string `json:"findings"`
}

func (s *Shell) SandboxStatus() SandboxStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := s.sandboxStatus
	status.Findings = append([]string(nil), status.Findings...)
	return status
}

func (s *Shell) sandboxTarget(item *session.Session) (string, bool) {
	s.mu.RLock()
	configured := config.Config{Sandbox: s.sandbox}
	s.mu.RUnlock()
	return configured.SandboxTarget(item.ID, item.SandboxMounts())
}

func (s *Shell) sandboxExecution(item *session.Session) (string, SandboxStatus, bool) {
	target, configured := s.sandboxTarget(item)
	return target, s.SandboxStatus(), configured
}

func probeSandboxStatus() SandboxStatus {
	key := os.Getenv("PATH") + "\x00" + os.Getenv("LOCALAPPDATA") + "\x00" + os.Getenv("ProgramFiles")
	sandboxProbeMu.Lock()
	defer sandboxProbeMu.Unlock()
	if status, ok := sandboxProbeCache[key]; ok {
		return status
	}
	status := probeSandboxStatusUncached()
	sandboxProbeCache[key] = status
	return status
}

func probeSandboxStatusUncached() SandboxStatus {
	status := SandboxStatus{Hypervisor: probeHypervisorPlatform()}
	executable, err := findSBX()
	if err != nil {
		status.Reason = "sbx is not installed"
		status.Findings = []string{"sandbox: sbx not installed", "sandbox: Windows Hypervisor Platform " + status.Hypervisor}
		return status
	}
	status.Installed, status.Executable = true, executable
	versionOutput, versionErr := shortCommand(executable, "version", "--json")
	if versionErr == nil {
		status.Version = strings.TrimSpace(versionOutput)
	}
	_, loginErr := shortCommand(executable, "ls", "--json")
	status.SignedIn = loginErr == nil
	if loginErr != nil {
		status.Reason = "sbx is installed but its operator Docker session is unavailable: " + oneLine(loginErr.Error())
	} else {
		status.Available = true
		status.Reason = "ready under the operator's Docker session"
	}
	version := status.Version
	if version == "" {
		version = "version unavailable"
	}
	login := "signed out"
	if status.SignedIn {
		login = "signed in"
	}
	status.Findings = []string{"sandbox: sbx " + version + " " + login, "sandbox: Windows Hypervisor Platform " + status.Hypervisor, "sandbox: execution identity is the operator's Docker session"}
	return status
}

func findSBX() (string, error) {
	if path, err := exec.LookPath("sbx"); err == nil {
		return path, nil
	}
	for _, candidate := range []string{
		filepath.Join(os.Getenv("LOCALAPPDATA"), "DockerSandboxes", "bin", "sbx.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "DockerSandboxes", "bin", "sbx.exe"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}

func shortCommand(executable string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, executable, args...).CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func probeHypervisorPlatform() string {
	hypervisorOnce.Do(func() { hypervisorState = readHypervisorPlatform() })
	return hypervisorState
}

func readHypervisorPlatform() string {
	if runtime.GOOS != "windows" {
		return "not required on this host"
	}
	output, err := shortCommand("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "(Get-CimInstance Win32_OptionalFeature -Filter \"Name='HypervisorPlatform'\").InstallState")
	if err != nil {
		return "state unavailable"
	}
	switch strings.TrimSpace(output) {
	case "1":
		return "on"
	case "2", "3":
		return "off"
	default:
		return "state unavailable"
	}
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func (s *Shell) callSandbox(ctx context.Context, item *session.Session, executable, sandboxID string, argv []string, input []byte, timeout int, cfg config.Shell) CallDetail {
	s.sandboxCreateMu.Lock()
	ensureErr := s.ensureSandbox(ctx, item, executable, sandboxID, timeout, cfg)
	s.sandboxCreateMu.Unlock()
	if ensureErr != nil {
		return CallDetail{Err: fmt.Errorf("target: sandbox %s; %w", sandboxID, ensureErr), Metadata: map[string]any{"target": "sandbox " + sandboxID}}
	}
	var output lockedBuffer
	process, _, err := s.startInput(cfg, executable, append([]string{"exec"}, argv...), input, item.Workspace, &output, true)
	if err != nil {
		return CallDetail{Err: fmt.Errorf("target: sandbox %s; %w", sandboxID, err), Metadata: map[string]any{"target": "sandbox " + sandboxID}}
	}
	detail := waitShellProcess(ctx, process, false, timeout, cfg, &output, executable)
	detail.Content = "target: sandbox " + sandboxID + "\n" + detail.Content
	detail.Metadata = map[string]any{"target": "sandbox " + sandboxID}
	return detail
}

func (s *Shell) ensureSandbox(ctx context.Context, item *session.Session, executable, sandboxID string, timeout int, cfg config.Shell) error {
	if output, err := shortCommand(executable, "ls", "-q"); err != nil {
		return fmt.Errorf("sandbox target is unavailable: %w", err)
	} else if !linePresent(output, sandboxID) {
		var createOutput lockedBuffer
		createArgs := []string{"create", "--name", sandboxID, "shell"}
		createArgs = append(createArgs, item.SandboxMounts()...)
		createProcess, _, err := s.startInput(cfg, executable, createArgs, nil, item.Workspace, &createOutput, true)
		if err != nil {
			return fmt.Errorf("create sandbox %s: %w", sandboxID, err)
		}
		if created := waitShellProcess(ctx, createProcess, false, timeout, cfg, &createOutput, executable); created.Err != nil {
			return fmt.Errorf("create sandbox %s: %w", sandboxID, created.Err)
		}
	}
	return nil
}

func linePresent(output, wanted string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == wanted {
			return true
		}
	}
	return false
}
