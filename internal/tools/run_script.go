package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"harness/internal/session"
)

type RunScript struct{ shell *Shell }

func NewRunScript(shell *Shell) *RunScript { return &RunScript{shell: shell} }
func (*RunScript) Name() string            { return "run_script" }
func (*RunScript) Description() string {
	return "Run source from standard input without creating a script file. Use powershell for multi-line PowerShell, python/node for host interpreter source, or bash when the global Docker Sandbox setting is enabled; script files are not created or executed."
}
func (*RunScript) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"language":  map[string]any{"type": "string", "enum": []string{"powershell", "python", "node", "bash"}},
			"source":    map[string]any{"type": "string"},
			"timeout_s": map[string]any{"type": "integer"},
		},
		"required": []string{"language", "source"},
	}
}
func (t *RunScript) Call(ctx context.Context, item *session.Session, args map[string]any) (string, error) {
	detail := t.CallDetailed(ctx, item, args)
	return detail.Content, detail.Err
}
func (t *RunScript) CallDetailed(ctx context.Context, item *session.Session, args map[string]any) CallDetail {
	return t.call(ctx, item, args, false)
}
func (t *RunScript) CallAsOperator(ctx context.Context, item *session.Session, args map[string]any) (string, error) {
	detail := t.call(ctx, item, args, true)
	return detail.Content, detail.Err
}
func (t *RunScript) call(ctx context.Context, item *session.Session, args map[string]any, forceOperator bool) (detail CallDetail) {
	if t == nil || t.shell == nil {
		return CallDetail{Err: fmt.Errorf("run_script runtime is unavailable")}
	}
	cfg := t.shell.config()
	defer func() { detail.OperatorContext = cfg.OperatorContext && !forceOperator }()
	language, _ := args["language"].(string)
	source, sourceOK := args["source"].(string)
	if !sourceOK || strings.TrimSpace(source) == "" {
		return CallDetail{Err: fmt.Errorf("source is required")}
	}
	if len(source) > 512*1024 {
		return CallDetail{Err: fmt.Errorf("source exceeds the 512 KB limit")}
	}
	if reason := forbiddenSigningCommand(source); reason != "" {
		return CallDetail{Err: fmt.Errorf("source blocked: %s", reason)}
	}
	if strings.EqualFold(strings.TrimSpace(language), "bash") {
		sandboxID, sandboxStatus, sandboxed := t.shell.sandboxExecution(item)
		if !sandboxed {
			return CallDetail{Err: fmt.Errorf("bash requires the global Docker Sandbox setting")}
		}
		if !sandboxStatus.Available {
			return CallDetail{Err: fmt.Errorf("Docker Sandbox is enabled but inert: %s", sandboxStatus.Reason)}
		}
		if !forceOperator && !cfg.OperatorContext {
			reason := "sandbox execution uses the operator's Docker session, outside the agentb-svc identity and firewall boundary"
			return CallDetail{Content: reason, OperatorOverrideReason: reason, Metadata: map[string]any{"target": "sandbox " + sandboxID}}
		}
		for _, denied := range cfg.Deny {
			if denied != "" && strings.Contains(strings.ToLower(source), strings.ToLower(denied)) {
				return CallDetail{Err: fmt.Errorf("source blocked by deny list"), Metadata: map[string]any{"target": "sandbox " + sandboxID}}
			}
		}
		timeout := number(args["timeout_s"], cfg.TimeoutS)
		if timeout <= 0 {
			timeout = cfg.TimeoutS
		}
		if timeout > cfg.MaxTimeoutS {
			timeout = cfg.MaxTimeoutS
		}
		return t.shell.callSandbox(ctx, item, sandboxStatus.Executable, sandboxID, []string{"-i", sandboxID, "bash", "-s"}, []byte(source), timeout, cfg)
	}
	var executable string
	var argv []string
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "powershell":
		executable = cfg.Command[0]
		argv = []string{"-NoProfile", "-NonInteractive", "-Command", "-"}
		if reason := forbiddenShellCommand(source, item, t.shell.fileCoordinatorSnapshot()); reason != "" {
			return CallDetail{Err: fmt.Errorf("source blocked: %s", reason)}
		}
	case "python":
		executable, argv = resolvedInterpreter("python"), []string{"-"}
	case "node":
		executable, argv = resolvedInterpreter("node"), []string{"-"}
	default:
		return CallDetail{Err: fmt.Errorf("language must be powershell, python, node, or bash")}
	}
	timeout := number(args["timeout_s"], cfg.TimeoutS)
	if timeout <= 0 {
		timeout = cfg.TimeoutS
	}
	if timeout > cfg.MaxTimeoutS {
		timeout = cfg.MaxTimeoutS
	}
	var output lockedBuffer
	process, usedService, err := t.shell.startInput(cfg, executable, argv, []byte(source), item.Workspace, &output, forceOperator)
	if err != nil {
		var required *operatorOverrideRequired
		if errors.As(err, &required) {
			return CallDetail{Content: "service-account script was not started: " + required.reason, OperatorOverrideReason: required.reason}
		}
		return CallDetail{Err: err}
	}
	return waitShellProcess(ctx, process, usedService, timeout, cfg, &output, executable)
}

func resolvedInterpreter(name string) string {
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	return name
}
