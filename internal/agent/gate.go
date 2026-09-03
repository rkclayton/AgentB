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
	mode := g.cfg().Approval.Mode
	return mode == "all" || mode == "mutating" && (name == "write_file" || name == "edit_file" || name == "shell")
}
func (g *Gate) Wait(ctx context.Context, s *session.Session, runID, callID, name string, args map[string]any) (bool, error) {
	if !g.required(name) {
		return true, nil
	}
	key := approvalKey(s.ID, callID)
	wait := approvalWait{decision: make(chan string, 1)}
	g.mu.Lock()
	g.waiting[key] = wait
	g.mu.Unlock()
	defer func() { g.mu.Lock(); delete(g.waiting, key); g.mu.Unlock() }()
	state := s.Snapshot(nil).Run
	state.Status = "paused"
	s.SetRun(state)
	g.bus.Publish(events.New(events.ApprovalRequired, s.ID, runID, map[string]any{"call_id": callID, "name": name, "args": args}))
	var decision string
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case decision = <-wait.decision:
	}
	g.bus.Publish(events.New(events.ApprovalDecided, s.ID, runID, map[string]any{"call_id": callID, "decision": decision}))
	state = s.Snapshot(nil).Run
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
