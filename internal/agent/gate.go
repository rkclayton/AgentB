package agent

import (
	"context"
	"fmt"
	"sync"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
)

type approvalWait struct {
	decision  chan approvalSignal
	scopeKind approvalScopeKind
	sessionID string
	runID     string
	callID    string
	decided   bool
}
type approvalSignal struct {
	decision string
	logged   bool
}
type approvalScopeKind int

const (
	approvalNoScopes approvalScopeKind = iota
	approvalShellScopes
	approvalFileScopes
	approvalPolicyScopes
	approvalCycle
)

type Gate struct {
	mu               sync.Mutex
	sequenceMu       sync.Mutex
	waiting          map[string]*approvalWait
	pendingBySession map[string]string
	bus              *events.Bus
	cfg              func() config.Config
}

func NewGate(bus *events.Bus, cfg func() config.Config) *Gate {
	return &Gate{waiting: map[string]*approvalWait{}, pendingBySession: map[string]string{}, bus: bus, cfg: cfg}
}
func approvalKey(sessionID, callID string) string { return sessionID + "\x00" + callID }
func (g *Gate) required(name string) bool {
	return approvalRequired(g.cfg().Approval.Mode, name)
}
func (g *Gate) requiredFor(s *session.Session, name string) bool {
	mode := s.Policy().ApprovalMode
	if mode == "" {
		mode = g.cfg().Approval.Mode
	}
	return approvalRequired(mode, name)
}
func approvalRequired(mode, name string) bool {
	switch mode {
	case config.ApprovalModeBoundaryOnly, config.ApprovalModeOff:
		return false
	case config.ApprovalModeMutating:
		return name == "write_file" || name == "edit_file" || name == "shell" || name == "run_script"
	case config.ApprovalModeAll:
		return true
	default:
		return false
	}
}
func (g *Gate) Wait(ctx context.Context, s *session.Session, runID, callID, name string, args map[string]any) (bool, error) {
	if !g.requiredFor(s, name) {
		return true, nil
	}
	return g.WaitPolicyRequired(ctx, s, runID, callID, name, args)
}

// WaitPolicyRequired always pauses for a policy decision, regardless of approval.mode.
// It does not grant operator identity and is not a boundary escape.
func (g *Gate) WaitPolicyRequired(ctx context.Context, s *session.Session, runID, callID, name string, args map[string]any) (bool, error) {
	decision, err := g.WaitPolicyDecision(ctx, s, runID, callID, name, args)
	return approvalGranted(decision), err
}

func (g *Gate) WaitPolicyDecision(ctx context.Context, s *session.Session, runID, callID, name string, args map[string]any) (string, error) {
	kind := approvalPolicyScopes
	if name == "shell" {
		kind = approvalShellScopes
	}
	g.sequenceMu.Lock()
	wait, cleanup := g.beginWait(s, runID, callID, kind)
	g.publishPolicyApprovalRequired(s, runID, callID, name, args)
	g.sequenceMu.Unlock()
	defer cleanup()
	return g.awaitDecision(ctx, s, runID, callID, wait)
}

// WaitBoundaryEscape always pauses for a user decision, regardless of approval.mode.
// It is used for identity escalation, never for a model-addressable tool.
func (g *Gate) WaitBoundaryEscape(ctx context.Context, s *session.Session, runID, callID, name string, args map[string]any) (bool, error) {
	decision, err := g.WaitBoundaryDecision(ctx, s, runID, callID, name, args)
	return approvalGranted(decision), err
}

func (g *Gate) WaitBoundaryDecision(ctx context.Context, s *session.Session, runID, callID, name string, args map[string]any) (string, error) {
	kind := approvalFileScopes
	if name == "shell.operator_override" || name == "shell.operator_command" {
		kind = approvalShellScopes
	}
	g.sequenceMu.Lock()
	wait, cleanup := g.beginWait(s, runID, callID, kind)
	g.publishBoundaryEscapeRequired(s, runID, callID, name, args)
	g.sequenceMu.Unlock()
	defer cleanup()
	return g.awaitDecision(ctx, s, runID, callID, wait)
}

func (g *Gate) WaitCycleDecision(ctx context.Context, s *session.Session, runID, callID string, args map[string]any) (string, error) {
	g.sequenceMu.Lock()
	wait, cleanup := g.beginWait(s, runID, callID, approvalCycle)
	g.bus.Publish(events.New(events.ApprovalRequired, s.ID, runID, map[string]any{
		"call_id": callID, "name": "run.cycle", "kind": "cycle", "args": args, "boundary_escape": false,
	}))
	g.sequenceMu.Unlock()
	defer cleanup()
	return g.awaitDecision(ctx, s, runID, callID, wait)
}

func (g *Gate) beginWait(s *session.Session, runID, callID string, kind approvalScopeKind) (*approvalWait, func()) {
	key := approvalKey(s.ID, callID)
	wait := &approvalWait{decision: make(chan approvalSignal, 1), scopeKind: kind, sessionID: s.ID, runID: runID, callID: callID}
	var superseded *approvalWait
	g.mu.Lock()
	if priorKey := g.pendingBySession[s.ID]; priorKey != "" && priorKey != key {
		if prior := g.waiting[priorKey]; prior != nil && !prior.decided {
			prior.decided = true
			superseded = prior
		}
		delete(g.waiting, priorKey)
	}
	g.waiting[key] = wait
	g.pendingBySession[s.ID] = key
	g.mu.Unlock()
	if superseded != nil {
		g.publishDecision(superseded, "superseded")
		superseded.decision <- approvalSignal{decision: "superseded", logged: true}
	}
	cleanup := func() {
		g.mu.Lock()
		if g.waiting[key] == wait {
			delete(g.waiting, key)
		}
		if g.pendingBySession[s.ID] == key {
			delete(g.pendingBySession, s.ID)
		}
		g.mu.Unlock()
	}
	state := s.Snapshot().Run
	state.Status = "paused"
	s.SetRun(state)
	return wait, cleanup
}

func (g *Gate) publishPolicyApprovalRequired(s *session.Session, runID, callID, name string, args map[string]any) {
	g.bus.Publish(events.New(events.ApprovalRequired, s.ID, runID, map[string]any{
		"call_id":         callID,
		"name":            name,
		"args":            args,
		"boundary_escape": false,
	}))
}

func (g *Gate) publishBoundaryEscapeRequired(s *session.Session, runID, callID, name string, args map[string]any) {
	g.bus.Publish(events.New(events.ApprovalRequired, s.ID, runID, map[string]any{
		"call_id":         callID,
		"name":            name,
		"args":            args,
		"boundary_escape": true,
	}))
}

func (g *Gate) awaitDecision(ctx context.Context, s *session.Session, runID, callID string, wait *approvalWait) (string, error) {
	var signal approvalSignal
	select {
	case <-ctx.Done():
		g.mu.Lock()
		if wait.decided {
			g.mu.Unlock()
			signal = <-wait.decision
		} else {
			wait.decided = true
			g.mu.Unlock()
			g.publishDecision(wait, "dismissed")
			return "dismissed", ctx.Err()
		}
	case signal = <-wait.decision:
	}
	if !signal.logged {
		g.publishDecision(wait, signal.decision)
	}
	if signal.decision == "superseded" {
		return signal.decision, fmt.Errorf("approval superseded by a newer decision")
	}
	if signal.decision != "superseded" && signal.decision != "dismissed" {
		state := s.Snapshot().Run
		state.Status = "running"
		s.SetRun(state)
	}
	return signal.decision, nil
}

func (g *Gate) publishDecision(wait *approvalWait, decision string) {
	if g.bus != nil {
		g.bus.Publish(events.New(events.ApprovalDecided, wait.sessionID, wait.runID, map[string]any{"call_id": wait.callID, "decision": decision}))
	}
}
func (g *Gate) Decide(sessionID, callID, decision string) error {
	return g.DecideWith(sessionID, callID, decision, nil)
}

func (g *Gate) DecideWith(sessionID, callID, decision string, before func()) error {
	if !validApprovalDecision(decision) {
		return fmt.Errorf("decision must be approve, once, run, session, operator_mode, or deny")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	wait, ok := g.waiting[approvalKey(sessionID, callID)]
	if !ok {
		return fmt.Errorf("approval not found")
	}
	if decision == "run" && wait.scopeKind != approvalShellScopes && wait.scopeKind != approvalFileScopes {
		return fmt.Errorf("decision %s requires a run-scoped shell or file-tool approval", decision)
	}
	if decision == "session" && wait.scopeKind == approvalNoScopes {
		return fmt.Errorf("decision %s requires a scoped approval", decision)
	}
	if decision == "operator_mode" && wait.scopeKind != approvalShellScopes {
		return fmt.Errorf("decision %s is only valid for a shell approval", decision)
	}
	if (decision == "continue" || decision == "stop") && wait.scopeKind != approvalCycle {
		return fmt.Errorf("decision %s is only valid for a cycle decision", decision)
	}
	if wait.scopeKind == approvalCycle && decision != "continue" && decision != "stop" {
		return fmt.Errorf("cycle decision must be continue or stop")
	}
	if wait.decided {
		return fmt.Errorf("approval already decided")
	}
	if before != nil {
		before()
	}
	wait.decided = true
	wait.decision <- approvalSignal{decision: decision}
	return nil
}

func validApprovalDecision(decision string) bool {
	switch decision {
	case "approve", "once", "run", "session", "operator_mode", "deny", "continue", "stop":
		return true
	default:
		return false
	}
}

func approvalGranted(decision string) bool {
	return decision == "approve" || decision == "once" || decision == "run" || decision == "session" || decision == "operator_mode"
}
