package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"harness/internal/events"
	"harness/internal/session"
	"harness/internal/tools"
)

const (
	shellGrantPolicy          = "shell_policy"
	shellGrantBoundary        = "shell_boundary"
	shellGrantOperatorCommand = "operator_command"
)

type shellRunGrant struct {
	Rule       string `json:"rule"`
	Identity   string `json:"identity"`
	Executable string `json:"executable,omitempty"`
}
type shellSessionGrant struct {
	shellRunGrant
	RunID string
}

func shellGrantKey(sessionID, runID string) string { return sessionID + "\x00" + runID }

func (r *Runner) hasShellGrant(sessionID, runID, rule, executable string) bool {
	r.shellGrantMu.Lock()
	defer r.shellGrantMu.Unlock()
	for _, grant := range r.shellGrants[shellGrantKey(sessionID, runID)] {
		if grant.Rule == rule && strings.EqualFold(grant.Executable, executable) {
			return true
		}
	}
	for _, stored := range r.shellSessionGrants[sessionID] {
		grant := stored.shellRunGrant
		if grant.Rule == rule && strings.EqualFold(grant.Executable, executable) {
			return true
		}
	}
	return false
}

func (r *Runner) grantShellSession(s *session.Session, runID string, grant shellRunGrant) {
	r.shellGrantMu.Lock()
	if r.shellSessionGrants == nil {
		r.shellSessionGrants = map[string][]shellSessionGrant{}
	}
	for _, existing := range r.shellSessionGrants[s.ID] {
		if existing.Rule == grant.Rule && strings.EqualFold(existing.Executable, grant.Executable) {
			r.shellGrantMu.Unlock()
			return
		}
	}
	r.shellSessionGrants[s.ID] = append(r.shellSessionGrants[s.ID], shellSessionGrant{shellRunGrant: grant, RunID: runID})
	r.shellGrantMu.Unlock()
	if r.bus != nil {
		r.bus.Publish(events.New(events.ShellGrant, s.ID, runID, shellGrantData(runID, "session", grant, "")))
	}
}

func (r *Runner) grantShellRun(s *session.Session, runID string, grant shellRunGrant) {
	key := shellGrantKey(s.ID, runID)
	r.shellGrantMu.Lock()
	if r.shellGrants == nil {
		r.shellGrants = map[string][]shellRunGrant{}
	}
	for _, existing := range r.shellGrants[key] {
		if existing.Rule == grant.Rule && strings.EqualFold(existing.Executable, grant.Executable) {
			r.shellGrantMu.Unlock()
			return
		}
	}
	r.shellGrants[key] = append(r.shellGrants[key], grant)
	r.shellGrantMu.Unlock()
	if r.bus != nil {
		r.bus.Publish(events.New(events.ShellGrant, s.ID, runID, shellGrantData(runID, "run", grant, "")))
	}
}

func (r *Runner) lapseShellGrants(s *session.Session, runID string) {
	key := shellGrantKey(s.ID, runID)
	r.shellGrantMu.Lock()
	grants := append([]shellRunGrant(nil), r.shellGrants[key]...)
	delete(r.shellGrants, key)
	r.shellGrantMu.Unlock()
	sort.Slice(grants, func(i, j int) bool {
		if grants[i].Rule == grants[j].Rule {
			return grants[i].Executable < grants[j].Executable
		}
		return grants[i].Rule < grants[j].Rule
	})
	for _, grant := range grants {
		if r.bus != nil {
			r.bus.Publish(events.New(events.ShellGrantLapsed, s.ID, runID, shellGrantData(runID, "run", grant, "run ended")))
		}
	}
}

// LapseSessionGrants closes only grants scoped to the durable chat. Run grants
// retain their existing run-end lifecycle.
func (r *Runner) LapseSessionGrants(sessionID string) {
	r.shellGrantMu.Lock()
	shellGrants := append([]shellSessionGrant(nil), r.shellSessionGrants[sessionID]...)
	delete(r.shellSessionGrants, sessionID)
	r.shellGrantMu.Unlock()
	sort.Slice(shellGrants, func(i, j int) bool {
		if shellGrants[i].Rule == shellGrants[j].Rule {
			return shellGrants[i].Executable < shellGrants[j].Executable
		}
		return shellGrants[i].Rule < shellGrants[j].Rule
	})
	for _, stored := range shellGrants {
		if r.bus != nil {
			r.bus.Publish(events.New(events.ShellGrantLapsed, sessionID, stored.RunID, shellGrantData(stored.RunID, "session", stored.shellRunGrant, "session closed")))
		}
	}

	r.fileGrantMu.Lock()
	fileRunID := r.fileSessionGrants[sessionID]
	delete(r.fileSessionGrants, sessionID)
	r.fileGrantMu.Unlock()
	if fileRunID != "" {
		r.publishFileGrant(events.FileGrantLapsed, sessionID, fileRunID, "session", "session closed")
	}
}

func shellGrantData(runID, scope string, grant shellRunGrant, reason string) map[string]any {
	data := map[string]any{"run_id": runID, "scope": scope, "rule": grant.Rule, "identity": grant.Identity}
	if grant.Executable != "" {
		data["executable"] = grant.Executable
	}
	if reason != "" {
		data["reason"] = reason
	}
	return data
}

func (r *Runner) executeOperatorCommand(ctx context.Context, s *session.Session, runID, callID, name string, args map[string]any, command tools.OperatorCommand) tools.CallOutcome {
	if !r.hasShellGrant(s.ID, runID, shellGrantOperatorCommand, command.Executable) {
		approvalArgs := map[string]any{
			"command": args["command"], "identity": "Agent_b operator (not Administrator)",
			"reason": fmt.Sprintf("operator command rule: %s runs as you for this run", command.Name),
			"rule":   shellGrantOperatorCommand, "executable": command.Executable,
			"scope": "run this configured operator command",
		}
		decision, err := r.gate.WaitBoundaryDecision(ctx, s, runID, callID, name+".operator_command", approvalArgs)
		if err != nil {
			return tools.CallOutcome{Content: "error: operator command canceled"}
		}
		if !approvalGranted(decision) {
			return tools.CallOutcome{Content: fmt.Sprintf("error: operator command rule: %s must run as the operator; denied by user and not run as the service account", command.Name)}
		}
		if decision == "run" {
			r.grantShellRun(s, runID, shellRunGrant{Rule: shellGrantOperatorCommand, Identity: "operator", Executable: command.Executable})
		}
		if decision == "session" {
			r.grantShellSession(s, runID, shellRunGrant{Rule: shellGrantOperatorCommand, Identity: "operator", Executable: command.Executable})
		}
	}
	return r.callConfiguredCommandAsOperator(ctx, s, name, args)
}

func (r *Runner) callConfiguredCommandAsOperator(ctx context.Context, s *session.Session, name string, args map[string]any) tools.CallOutcome {
	executionArgs := args
	cfg := r.cfg()
	if command, ok := args["command"].(string); ok && isPowerShellCommand(cfg.Shell.Command) {
		executionArgs = make(map[string]any, len(args))
		for key, value := range args {
			executionArgs[key] = value
		}
		executionArgs["command"] = command + `; if ($null -ne $LASTEXITCODE) { exit $LASTEXITCODE }`
	}
	return r.callShellAsOperator(ctx, s, name, executionArgs)
}

func isPowerShellCommand(command []string) bool {
	if len(command) == 0 {
		return false
	}
	name := strings.ToLower(strings.ReplaceAll(command[0], `\`, "/"))
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	return name == "powershell" || name == "powershell.exe" || name == "pwsh" || name == "pwsh.exe"
}

func (r *Runner) callShellAsOperator(ctx context.Context, s *session.Session, name string, args map[string]any) tools.CallOutcome {
	if r.toolActivity != nil {
		r.toolActivity("started")
		defer r.toolActivity("completed")
	}
	content, ok := r.tools.CallAsOperator(ctx, s, name, args)
	if ok && strings.TrimSpace(content) == "" {
		content = "the tool completed with no output"
	}
	return tools.CallOutcome{Content: content, OK: ok, OperatorContext: true}
}
