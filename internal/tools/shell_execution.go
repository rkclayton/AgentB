package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"harness/internal/config"
	"harness/internal/session"
)

// Shell executes commands without a workspace jail or OS sandbox. The workspace is
// only its initial working directory; identity, approval, timeout, and deny-list
// controls do not make it workspace-confined.
type Shell struct {
	mu                sync.RWMutex
	cfg               config.Shell
	operatorCommands  []string
	workspace         string
	sandbox           config.Sandbox
	sandboxStatus     SandboxStatus
	sandboxCreateMu   sync.Mutex
	fileCoordinator   *FileCoordinator
	credential        shellCredentialReader
	startService      serviceProcessStarter
	startServiceInput serviceProcessInputStarter
	identityMu        sync.RWMutex
	identity          ShellIdentityStatus
	identityReporter  func(ShellIdentityStatus)
}

func NewShell(cfg config.Shell) *Shell {
	return &Shell{cfg: cfg, startService: startServiceAccountProcess, startServiceInput: startServiceAccountProcessWithInput}
}
func (*Shell) Name() string { return "shell" }
func (s *Shell) Description() string {
	cfg, operatorCommands := s.configWithOperatorCommands()
	dialect := "the configured shell"
	if len(cfg.Command) > 0 {
		dialect = shellDialect(cfg.Command[0])
	}
	syntax := "Use " + dialect + " syntax."
	if len(cfg.Command) > 0 && shellCommandName(cfg.Command[0]) == "powershell" {
		syntax = "Windows PowerShell 5: use `;` to chain commands, not `&&`."
	}
	description := "Run an unconfined inline command from the folder root. Shell has no network in service context (enforced outside the tool layer); use fetch_url for every network operation. Agent-written Windows host scripts cannot be executed; use run_script for multi-line source. " + syntax
	if cfg.ServiceAccount.Enabled && len(operatorCommands) > 0 {
		description += " Git and other configured operator commands run as the operator after one decision per run; expect one prompt, not one per call."
	}
	return description
}
func (s *Shell) Schema() map[string]any {
	cfg := s.config()
	return map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}, "timeout_s": map[string]any{"type": "integer", "default": cfg.TimeoutS, "maximum": cfg.MaxTimeoutS}}, "required": []string{"command"}}
}
func (s *Shell) Call(ctx context.Context, item *session.Session, args map[string]any) (string, error) {
	detail := s.CallDetailed(ctx, item, args)
	return detail.Content, detail.Err
}

func (s *Shell) CallDetailed(ctx context.Context, item *session.Session, args map[string]any) CallDetail {
	return s.call(ctx, item, args, false)
}

// CallAsOperator is intentionally absent from the Tool interface and schema.
// Only the dispatcher invokes it after a separate, unconditional approval.
func (s *Shell) CallAsOperator(ctx context.Context, item *session.Session, args map[string]any) (string, error) {
	detail := s.call(ctx, item, args, true)
	return detail.Content, detail.Err
}

func (s *Shell) call(ctx context.Context, item *session.Session, args map[string]any, forceOperator bool) (detail CallDetail) {
	cfg := s.config()
	defer func() { detail.OperatorContext = cfg.OperatorContext && !forceOperator }()
	command, ok := args["command"].(string)
	if !ok || strings.TrimSpace(command) == "" {
		return CallDetail{Err: fmt.Errorf("command is required")}
	}
	if reason := forbiddenShellCommand(command, item, s.fileCoordinatorSnapshot()); reason != "" {
		return CallDetail{Err: fmt.Errorf("command blocked: %s", reason)}
	}
	sandboxID, sandboxStatus, sandboxed := s.sandboxExecution(item)
	if sandboxed && !sandboxStatus.Available {
		sandboxed = false
	}
	if sandboxed && !forceOperator && !cfg.OperatorContext {
		reason := "sandbox execution uses the operator's Docker session, outside the agentb-svc identity and firewall boundary"
		return CallDetail{Content: reason, OperatorOverrideReason: reason, Metadata: map[string]any{"target": "sandbox " + sandboxID}}
	}
	if sandboxed {
		for _, denied := range cfg.Deny {
			if denied != "" && strings.Contains(strings.ToLower(command), strings.ToLower(denied)) {
				return CallDetail{Err: fmt.Errorf("command blocked by deny list"), Metadata: map[string]any{"target": "sandbox " + sandboxID}}
			}
		}
		timeout := number(args["timeout_s"], cfg.TimeoutS)
		if timeout <= 0 {
			timeout = cfg.TimeoutS
		}
		if timeout > cfg.MaxTimeoutS {
			timeout = cfg.MaxTimeoutS
		}
		return s.callSandbox(ctx, item, sandboxStatus.Executable, sandboxID, []string{sandboxID, "bash", "-lc", command}, nil, timeout, cfg)
	}
	if cfg.FileRoutingGuardEnabled() {
		refusal, ambiguous := inspectShellFileRouting(command)
		if refusal != nil {
			if cfg.ServiceAccount.Enabled && refusal.Replacement.Tool == "find_files" && routingReplacementOutsideWorkspace(item.Workspace, refusal) {
				refusal.Reason = "direct file discovery path is outside the folder while the service-account split is enabled"
				refusal.Replacement = nil
				refusal.Guidance = "paths outside the folder require an operator decision; state the need once and stop rather than retrying paths"
			}
			result, _ := json.Marshal(refusal)
			replacement := "none"
			if refusal.Replacement != nil {
				replacement = refusal.Replacement.Tool
			}
			log.Printf("shell file-routing refusal: session=%s tool=%s command=%q", item.ID, replacement, command)
			return CallDetail{Err: fmt.Errorf("note: command was not executed; %s", result)}
		}
		if ambiguous {
			log.Printf("debug: shell file-routing guard allowed ambiguous compound command: %q", command)
		}
	}
	for _, denied := range cfg.Deny {
		if denied != "" && strings.Contains(strings.ToLower(command), strings.ToLower(denied)) {
			return CallDetail{Err: fmt.Errorf("command blocked by deny list")}
		}
	}
	timeout := number(args["timeout_s"], cfg.TimeoutS)
	if timeout <= 0 {
		timeout = cfg.TimeoutS
	}
	if timeout > cfg.MaxTimeoutS {
		timeout = cfg.MaxTimeoutS
	}
	argv := append(append([]string(nil), cfg.Command[1:]...), command)
	var output lockedBuffer
	var process runningShellProcess
	var usedService bool
	var err error
	if !forceOperator && cfg.ServiceAccount.Enabled && !cfg.OperatorContext {
		if name := operatorOnlyInterpreter(command, exec.LookPath, operatorHome()); name != "" {
			reason := name + " is operator-only; needs Run as you"
			return CallDetail{Content: reason, OperatorOverrideReason: reason}
		}
	}
	if forceOperator {
		process, err = startHarnessProcess(cfg.Command[0], argv, item.Workspace, &output)
	} else {
		process, usedService, err = s.start(cfg, item.Workspace, argv, &output)
	}
	if err != nil {
		var required *operatorOverrideRequired
		if errors.As(err, &required) {
			return CallDetail{
				Content:                "service-account shell was not started: " + required.reason,
				OperatorOverrideReason: required.reason,
			}
		}
		return CallDetail{Err: err}
	}
	return waitShellProcess(ctx, process, usedService, timeout, cfg, &output, command)
}

func waitShellProcess(ctx context.Context, process runningShellProcess, usedService bool, timeout int, cfg config.Shell, output *lockedBuffer, operatorCommand string) CallDetail {
	type waitResult struct {
		code int
		err  error
	}
	done := make(chan waitResult, 1)
	go func() {
		code, err := process.Wait()
		done <- waitResult{code: code, err: err}
	}()
	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		process.KillTree()
		<-done
		return CallDetail{Err: ctx.Err()}
	case <-timer.C:
		process.KillTree()
		<-done
		partial := cutOutput(output.String(), cfg.MaxOutputLinesHead, cfg.MaxOutputLinesTail)
		if partial != "" {
			return CallDetail{Err: fmt.Errorf("timed out after %ds; partial output:\n%s", timeout, partial)}
		}
		return CallDetail{Err: fmt.Errorf("timed out after %ds; partial output:", timeout)}
	case result := <-done:
		if result.err != nil {
			return CallDetail{Err: result.err}
		}
		body := cutOutput(output.String(), cfg.MaxOutputLinesHead, cfg.MaxOutputLinesTail)
		if result.code != 0 {
			content := fmt.Sprintf("exit=%d", result.code)
			if body != "" {
				content += "\n" + body
			}
			if usedService {
				if reason := serviceBoundaryReason(operatorCommand, body); reason != "" {
					return CallDetail{Content: content, OperatorOverrideReason: reason}
				}
			}
			return CallDetail{Err: fmt.Errorf("command failed\n%s", content)}
		}
		if body == "" {
			return CallDetail{Content: "exit=0"}
		}
		return CallDetail{Content: "exit=0\n" + body}
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *lockedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }
func cutOutput(value string, head, tail int) string {
	value = strings.TrimRight(normalizeLF(value), "\n")
	if value == "" {
		return ""
	}
	lines := strings.Split(value, "\n")
	if len(lines) <= head+tail {
		return value
	}
	omitted := len(lines) - head - tail
	kept := append([]string(nil), lines[:head]...)
	kept = append(kept, fmt.Sprintf("[… %d lines omitted …]", omitted))
	kept = append(kept, lines[len(lines)-tail:]...)
	return strings.Join(kept, "\n")
}
