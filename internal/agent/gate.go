package agent

import (
	"context"
	"fmt"
	"sync"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
)

type approvalWait struct{ decision chan string }
type Gate struct {
	mu      sync.Mutex
	waiting map[string]approvalWait
	bus     *events.Bus
	cfg     func() config.Config
}

func NewGate(bus *events.Bus, cfg func() config.Config) *Gate {
	return &Gate{waiting: map[string]approvalWait{}, bus: bus, cfg: cfg}
}
func approvalKey(sessionID, callID string) string { return sessionID + "\x00" + callID }
func (g *Gate) required(name string) bool {
	switch g.cfg().Approval.Mode {
	case config.ApprovalModeBoundaryOnly, config.ApprovalModeOff:
		return false
	case config.ApprovalModeMutating:
		return name == "write_file" || name == "edit_file" || name == "shell"
	case config.ApprovalModeAll:
		return true
	default:
		return false
	}
}
func (g *Gate) Wait(ctx context.Context, s *session.Session, runID, callID, name string, args map[string]any) (bool, error) {
	if !g.required(name) {
		return true, nil
	}
	return g.WaitPolicyRequired(ctx, s, runID, callID, name, args)
}

// WaitPolicyRequired always pauses for a policy decision, regardless of approval.mode.
// It does not grant operator identity and is not a boundary escape.
func (g *Gate) WaitPolicyRequired(ctx context.Context, s *session.Session, runID, callID, name string, args map[string]any) (bool, error) {
	wait, cleanup := g.beginWait(s, callID)
	defer cleanup()
	g.publishPolicyApprovalRequired(s, runID, callID, name, args)
	return g.awaitDecision(ctx, s, runID, callID, wait)
}

// WaitBoundaryEscape always pauses for a user decision, regardless of approval.mode.
// It is used for identity escalation, never for a model-addressable tool.
func (g *Gate) WaitBoundaryEscape(ctx context.Context, s *session.Session, runID, callID, name string, args map[string]any) (bool, error) {
	wait, cleanup := g.beginWait(s, callID)
	defer cleanup()
	g.publishBoundaryEscapeRequired(s, runID, callID, name, args)
	return g.awaitDecision(ctx, s, runID, callID, wait)
}

func (g *Gate) beginWait(s *session.Session, callID string) (approvalWait, func()) {
	key := approvalKey(s.ID, callID)
	wait := approvalWait{decision: make(chan string, 1)}
	g.mu.Lock()
	g.waiting[key] = wait
	g.mu.Unlock()
	cleanup := func() {
		g.mu.Lock()
		delete(g.waiting, key)
		g.mu.Unlock()
	}
	state := s.Snapshot(nil).Run
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

func (g *Gate) awaitDecision(ctx context.Context, s *session.Session, runID, callID string, wait approvalWait) (bool, error) {
	var decision string
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case decision = <-wait.decision:
	}
	g.bus.Publish(events.New(events.ApprovalDecided, s.ID, runID, map[string]any{"call_id": callID, "decision": decision}))
	state := s.Snapshot(nil).Run
	state.Status = "running"
	s.SetRun(state)
	return decision == "approve", nil
}
func (g *Gate) Decide(sessionID, callID, decision string) error {
	if decision != "approve" && decision != "deny" {
		return fmt.Errorf("decision must be approve or deny")
	}
	g.mu.Lock()
	wait, ok := g.waiting[approvalKey(sessionID, callID)]
	g.mu.Unlock()
	if !ok {
		return fmt.Errorf("approval not found")
	}
	select {
	case wait.decision <- decision:
		return nil
	default:
		return fmt.Errorf("approval already decided")
	}
}
