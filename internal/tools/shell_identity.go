package tools

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"harness/internal/config"
	"harness/internal/credential"
)

type shellCredentialReader interface {
	Read() ([]byte, error)
}

type ShellIdentityStatus struct {
	Fallback                 bool   `json:"fallback"`
	OperatorApprovalRequired bool   `json:"operator_approval_required"`
	OperatorContext          bool   `json:"operator_context"`
	OperatorContextExpiresAt string `json:"operator_context_expires_at"`
	Reason                   string `json:"reason"`
	Since                    string `json:"since"`
}

type operatorOverrideRequired struct{ reason string }

func (e *operatorOverrideRequired) Error() string { return e.reason }

func (s *Shell) OperatorCommand(args map[string]any) (OperatorCommand, bool) {
	return s.OperatorCommandWith(args, nil)
}
func (s *Shell) OperatorCommandWith(args map[string]any, additional []string) (OperatorCommand, bool) {
	command, _ := args["command"].(string)
	segments := splitShellCommands(command)
	if len(segments) == 0 {
		return OperatorCommand{}, false
	}
	words := shellWords(segments[0])
	for len(words) > 0 && (words[0] == "&" || strings.EqualFold(words[0], "call")) {
		words = words[1:]
	}
	if len(words) == 0 {
		return OperatorCommand{}, false
	}
	resolved, ok := resolveOperatorExecutable(words[0])
	if !ok {
		return OperatorCommand{}, false
	}
	_, configured := s.configWithOperatorCommands()
	configured = append(configured, additional...)
	for _, candidate := range configured {
		configuredPath, found := resolveOperatorExecutable(candidate)
		if found && strings.EqualFold(configuredPath, resolved) {
			return OperatorCommand{Name: shellCommandName(resolved), Executable: resolved}, true
		}
	}
	return OperatorCommand{}, false
}

func resolveOperatorExecutable(value string) (string, bool) {
	value = cleanShellScriptToken(value)
	if value == "" || strings.ContainsAny(value, "$%*?`") {
		return "", false
	}
	resolved, err := exec.LookPath(value)
	if err != nil {
		return "", false
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", false
	}
	if evaluated, evalErr := filepath.EvalSymlinks(absolute); evalErr == nil {
		absolute = evaluated
	}
	info, err := os.Stat(absolute)
	if err != nil || info.IsDir() {
		return "", false
	}
	return filepath.Clean(absolute), true
}

func operatorHome() string {
	home, _ := os.UserHomeDir()
	return home
}

func operatorOnlyInterpreter(command string, lookPath func(string) (string, error), home string) string {
	if home == "" {
		return ""
	}
	home = strings.ToLower(filepath.Clean(home)) + string(os.PathSeparator)
	for _, token := range shellPolicyTokens(command) {
		cleaned := cleanShellScriptToken(token)
		candidate := strings.ToLower(filepath.Base(cleaned))
		candidate = strings.TrimSuffix(candidate, filepath.Ext(candidate))
		if candidate == "py" {
			candidate = "python"
		}
		if candidate != "python" && candidate != "node" && candidate != "go" && candidate != "dotnet" {
			continue
		}
		resolved, err := lookPath(cleaned)
		if err == nil && strings.HasPrefix(strings.ToLower(filepath.Clean(resolved)), home) {
			return candidate
		}
	}
	return ""
}

func serviceBoundaryReason(operatorCommand, output string) string {
	if permissionDeniedOutput(output) {
		return "service account was denied permission"
	}
	lower := strings.ToLower(output)
	if !strings.Contains(lower, "is not recognized as the name of a") || !strings.Contains(lower, "cmdlet") {
		return ""
	}
	for _, token := range shellPolicyTokens(operatorCommand) {
		candidate := cleanShellScriptToken(token)
		if filepath.IsAbs(candidate) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return "service account could not access an operator-visible executable"
			}
		}
	}
	return ""
}

func (s *Shell) start(cfg config.Shell, workspace string, argv []string, output *lockedBuffer) (runningShellProcess, bool, error) {
	service := cfg.ServiceAccount
	if cfg.OperatorContext || !service.Enabled {
		process, err := startHarnessProcess(cfg.Command[0], argv, workspace, output)
		return process, false, err
	}
	reason := ""
	if s.credential == nil {
		reason = "service-account credential is not configured"
	} else {
		password, err := s.credential.Read()
		if err == nil {
			defer clearBytes(password)
			process, spawnErr := s.startService(cfg.Command[0], argv, workspace, minimalShellEnvironment(service, workspace), service, password, output)
			if spawnErr == nil {
				s.setIdentity(s.configuredIdentityStatus())
				return process, true, nil
			}
			reason = serviceSpawnReason(spawnErr)
		} else if errors.Is(err, credential.ErrNotStored) {
			reason = "service-account credential is not stored"
		} else {
			reason = "service-account credential cannot be decrypted by this Agent_b process identity"
		}
	}
	s.setIdentity(ShellIdentityStatus{OperatorApprovalRequired: true, Reason: reason, Since: time.Now().UTC().Format(time.RFC3339)})
	log.Printf("ALARM: shell service-account spawn failed; operator approval required: %s", reason)
	return nil, false, &operatorOverrideRequired{reason: reason}
}

func (s *Shell) startInput(cfg config.Shell, executable string, argv []string, input []byte, workspace string, output *lockedBuffer, forceOperator bool) (runningShellProcess, bool, error) {
	if forceOperator || cfg.OperatorContext || !cfg.ServiceAccount.Enabled {
		process, err := startHarnessProcessWithInput(executable, argv, workspace, input, output)
		return process, false, err
	}
	reason := ""
	if s.credential == nil {
		reason = "service-account credential is not configured"
	} else {
		password, err := s.credential.Read()
		if err == nil {
			defer clearBytes(password)
			process, spawnErr := s.startServiceInput(executable, argv, workspace, minimalShellEnvironment(cfg.ServiceAccount, workspace), cfg.ServiceAccount, password, input, output)
			if spawnErr == nil {
				s.setIdentity(s.configuredIdentityStatus())
				return process, true, nil
			}
			reason = serviceSpawnReason(spawnErr)
		} else if errors.Is(err, credential.ErrNotStored) {
			reason = "service-account credential is not stored"
		} else {
			reason = "service-account credential cannot be decrypted by this Agent_b process identity"
		}
	}
	s.setIdentity(ShellIdentityStatus{OperatorApprovalRequired: true, Reason: reason, Since: time.Now().UTC().Format(time.RFC3339)})
	log.Printf("ALARM: run_script service-account spawn failed; operator approval required: %s", reason)
	return nil, false, &operatorOverrideRequired{reason: reason}
}

func permissionDeniedOutput(output string) bool {
	value := strings.ToLower(output)
	for _, marker := range []string{
		"access is denied",
		"permission denied",
		"unauthorizedaccessexception",
		"attempted to perform an unauthorized operation",
		"requested operation requires elevation",
		"requires elevation",
		"operation not permitted",
		"administrator privileges are required",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return strings.Contains(value, "access to") && strings.Contains(value, "is denied")
}

func (s *Shell) TestServiceAccount(ctx context.Context) (string, error) {
	cfg, workspace := s.configWithWorkspace()
	workspace, err := usableShellWorkspace(workspace)
	if err != nil {
		return s.failedServiceTest("service-account working directory is unavailable", err)
	}
	if s.credential == nil {
		return s.failedServiceTest("service-account credential is not configured", errors.New("service-account credential is not configured"))
	}
	password, err := s.credential.Read()
	if errors.Is(err, credential.ErrNotStored) {
		return s.failedServiceTest("service-account credential is not stored", err)
	}
	if err != nil {
		return s.failedServiceTest("service-account credential cannot be decrypted by this Agent_b process identity", err)
	}
	defer clearBytes(password)
	command := shellNoop(cfg.Command[0])
	argv := append(append([]string(nil), cfg.Command[1:]...), command)
	var output lockedBuffer
	process, err := s.startService(cfg.Command[0], argv, workspace, minimalShellEnvironment(cfg.ServiceAccount, workspace), cfg.ServiceAccount, password, &output)
	if err != nil {
		return s.failedServiceTest(serviceSpawnReason(err), err)
	}
	type waitResult struct {
		code int
		err  error
	}
	done := make(chan waitResult, 1)
	go func() {
		code, waitErr := process.Wait()
		done <- waitResult{code: code, err: waitErr}
	}()
	select {
	case <-ctx.Done():
		process.KillTree()
		<-done
		return s.failedServiceTest("service-account test timed out", ctx.Err())
	case result := <-done:
		if result.err != nil {
			return s.failedServiceTest("service-account test process failed", result.err)
		}
		if result.code != 0 {
			return s.failedServiceTest(fmt.Sprintf("service-account test process exited %d", result.code), errors.New("test process returned nonzero exit"))
		}
		s.setIdentity(s.configuredIdentityStatus())
		return "service-account shell spawn succeeded", nil
	}
}

func (s *Shell) failedServiceTest(reason string, err error) (string, error) {
	status := ShellIdentityStatus{OperatorApprovalRequired: true, Reason: reason, Since: time.Now().UTC().Format(time.RFC3339)}
	if s.config().OperatorContext {
		status = s.configuredIdentityStatus()
	}
	s.setIdentity(status)
	log.Printf("ALARM: service-account test failed; operator approval required: %s", reason)
	return reason, err
}

func (s *Shell) SetCredentialStore(reader shellCredentialReader) {
	s.mu.Lock()
	s.credential = reader
	s.mu.Unlock()
}

func (s *Shell) SetFileCoordinator(coordinator *FileCoordinator) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fileCoordinator = coordinator
}

func (s *Shell) fileCoordinatorSnapshot() *FileCoordinator {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fileCoordinator
}

func (s *Shell) SetIdentityReporter(reporter func(ShellIdentityStatus)) {
	s.identityMu.Lock()
	s.identityReporter = reporter
	s.identityMu.Unlock()
}

func (s *Shell) IdentityStatus() ShellIdentityStatus {
	s.identityMu.RLock()
	defer s.identityMu.RUnlock()
	return s.identity
}

func (s *Shell) setIdentity(status ShellIdentityStatus) {
	s.identityMu.Lock()
	if (status.Fallback || status.OperatorApprovalRequired) &&
		(s.identity.Fallback || s.identity.OperatorApprovalRequired) &&
		s.identity.Reason == status.Reason {
		status.Since = s.identity.Since
	}
	s.identity = status
	reporter := s.identityReporter
	s.identityMu.Unlock()
	if reporter != nil {
		reporter(status)
	}
}

func (s *Shell) Configure(value config.Config) {
	workspace := value.Workspace
	if absolute, err := filepath.Abs(workspace); err == nil {
		workspace = absolute
	}
	sandboxStatus := probeSandboxStatus()
	s.mu.Lock()
	s.cfg = value.Shell
	s.sandbox = value.Sandbox
	s.sandboxStatus = sandboxStatus
	s.operatorCommands = append([]string(nil), value.Tools.Shell.OperatorCommands...)
	s.workspace = workspace
	s.mu.Unlock()
	if value.Shell.OperatorContext {
		s.setIdentity(s.configuredIdentityStatus())
	} else if !value.Shell.ServiceAccount.Enabled || s.IdentityStatus().OperatorContext {
		s.setIdentity(ShellIdentityStatus{})
	}
}

func (s *Shell) configuredIdentityStatus() ShellIdentityStatus {
	if !s.config().OperatorContext {
		return ShellIdentityStatus{}
	}
	return ShellIdentityStatus{
		OperatorContext:          true,
		OperatorContextExpiresAt: s.config().OperatorContextExpiresAt,
		Reason:                   "operator context is enabled; tools are running as the Windows account that launched Agent_b",
		Since:                    time.Now().UTC().Format(time.RFC3339),
	}
}

func usableShellWorkspace(workspace string) (string, error) {
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve shell folder: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("open shell folder %q: %w", absolute, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("shell folder %q is not a directory", absolute)
	}
	return absolute, nil
}

func (s *Shell) config() config.Shell { s.mu.RLock(); defer s.mu.RUnlock(); return s.cfg }
func (s *Shell) configWithOperatorCommands() (config.Shell, []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg, append([]string(nil), s.operatorCommands...)
}
func (s *Shell) configWithWorkspace() (config.Shell, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg, s.workspace
}

func clearBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func shellNoop(executable string) string {
	name := shellCommandName(executable)
	if name == "cmd" {
		return "exit 0"
	}
	if name == "sh" || name == "bash" || name == "zsh" {
		return ":"
	}
	return "$null"
}
