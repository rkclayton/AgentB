package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
)

type queuedRun struct {
	s                    *session.Session
	runID, userMessageID string
	userMessage          events.Message
}
type activeRun struct {
	cancel     context.CancelFunc
	stopReason string
	runID      string
	done       chan struct{}
}
type SubmitResult struct {
	RunID    string `json:"run_id"`
	Queued   bool   `json:"queued,omitempty"`
	Position int    `json:"position,omitempty"`
}
type Scheduler struct {
	mu          sync.Mutex
	runner      *Runner
	registry    *session.Registry
	bus         *events.Bus
	cfg         func() config.Config
	active      map[string]*activeRun
	queue       []queuedRun
	pending     map[string][]queuedRun
	held        map[string]bool
	unreachable map[string]bool
	ids         atomic.Int64
	agentIdle   func(string)
}

func NewScheduler(runner *Runner, registry *session.Registry, bus *events.Bus, cfg func() config.Config) *Scheduler {
	return &Scheduler{runner: runner, registry: registry, bus: bus, cfg: cfg, active: map[string]*activeRun{}, pending: map[string][]queuedRun{}, held: map[string]bool{}, unreachable: map[string]bool{}}
}
func (s *Scheduler) SetAgentIdleCallback(callback func(string)) {
	s.mu.Lock()
	s.agentIdle = callback
	s.mu.Unlock()
}
func (s *Scheduler) TryAgentIdle(agentID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agentActiveLocked(agentID) {
		return false
	}
	if s.agentIdle != nil {
		s.agentIdle(agentID)
	}
	return true
}
func (s *Scheduler) agentActiveLocked(agentID string) bool {
	for sessionID := range s.active {
		if item, ok := s.registry.Get(sessionID); ok && item.Snapshot().AgentID == agentID {
			return true
		}
	}
	return false
}
func (s *Scheduler) notifyAgentIdleLocked(agentID string) {
	if agentID != "" && !s.agentActiveLocked(agentID) && s.agentIdle != nil {
		s.agentIdle(agentID)
	}
}
func (s *Scheduler) Submit(ctx context.Context, sessionID, text string) (SubmitResult, error) {
	return s.SubmitAttachments(ctx, sessionID, text, nil)
}
func (s *Scheduler) SubmitAttachments(ctx context.Context, sessionID, text string, attachments []events.Attachment) (SubmitResult, error) {
	item, ok := s.registry.Get(sessionID)
	if !ok {
		return SubmitResult{}, fmt.Errorf("session not found")
	}
	if item.IsClosed() {
		return SubmitResult{}, fmt.Errorf("session is closed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !item.BeginSubmission() {
		return SubmitResult{}, fmt.Errorf("session is closed")
	}
	defer item.EndSubmission()
	if s.unreachable[sessionID] {
		if position := identicalPendingPosition(s.pending[sessionID], text); position > 0 {
			return SubmitResult{Queued: true, Position: position}, nil
		}
		message, err := s.runner.QueueUserAttachments(ctx, item, text, attachments)
		if err != nil {
			return SubmitResult{}, err
		}
		s.pending[sessionID] = append(s.pending[sessionID], queuedRun{s: item, userMessageID: message.ID, userMessage: message})
		position := len(s.pending[sessionID])
		item.SetQueuedMessages(position)
		state := item.Snapshot().Run
		state.Status = "held"
		state.QueuePosition = position
		item.SetRun(state)
		s.bus.Publish(events.New(events.MessageQueued, sessionID, "", map[string]any{"message_id": message.ID, "position": position, "waiting_for": "model"}))
		return SubmitResult{Queued: true, Position: position}, nil
	}
	if s.held[sessionID] {
		message, err := s.runner.QueueUserAttachments(ctx, item, text, attachments)
		if err != nil {
			return SubmitResult{}, err
		}
		s.pending[sessionID] = append(s.pending[sessionID], queuedRun{s: item, userMessageID: message.ID, userMessage: message})
		position := len(s.pending[sessionID])
		delete(s.held, sessionID)
		next := s.pending[sessionID][0]
		s.pending[sessionID] = s.pending[sessionID][1:]
		item.SetQueuedMessages(len(s.pending[sessionID]))
		next.runID = fmt.Sprintf("r%d", s.ids.Add(1))
		if len(s.active) < s.cfg().Run.MaxConcurrent {
			s.startLocked(next)
		} else {
			s.queue = append(s.queue, next)
			item.SetRun(session.RunState{Status: "queued", RunID: next.runID, MaxTurns: s.cfg().Run.MaxTurns, QueuePosition: len(s.queue)})
			s.bus.Publish(events.New(events.RunQueued, sessionID, next.runID, map[string]any{"run_id": next.runID, "position": len(s.queue)}))
		}
		return SubmitResult{Queued: true, Position: position}, nil
	}
	if _, active := s.active[sessionID]; active || item.IsRunning() {
		if position := identicalPendingPosition(s.pending[sessionID], text); position > 0 {
			return SubmitResult{Queued: true, Position: position}, nil
		}
		depth := s.cfg().Run.QueueDepth
		if depth > 0 && len(s.pending[sessionID]) >= depth {
			return SubmitResult{}, fmt.Errorf("queue full")
		}
		message, err := s.runner.QueueUserAttachments(ctx, item, text, attachments)
		if err != nil {
			return SubmitResult{}, err
		}
		s.pending[sessionID] = append(s.pending[sessionID], queuedRun{s: item, userMessageID: message.ID, userMessage: message})
		position := len(s.pending[sessionID])
		item.SetQueuedMessages(position)
		s.bus.Publish(events.New(events.MessageQueued, sessionID, "", map[string]any{"message_id": message.ID, "position": position}))
		return SubmitResult{Queued: true, Position: position}, nil
	}
	message, err := s.runner.AddUserAttachments(ctx, item, text, attachments)
	if err != nil {
		return SubmitResult{}, err
	}
	runID := fmt.Sprintf("r%d", s.ids.Add(1))
	if len(s.active) < s.cfg().Run.MaxConcurrent {
		s.startLocked(queuedRun{s: item, runID: runID, userMessageID: message.ID})
		return SubmitResult{RunID: runID}, nil
	}
	entry := queuedRun{s: item, runID: runID, userMessageID: message.ID}
	s.queue = append(s.queue, entry)
	position := len(s.queue)
	item.SetRun(session.RunState{Status: "queued", RunID: runID, MaxTurns: s.cfg().Run.MaxTurns, QueuePosition: position})
	s.bus.Publish(events.New(events.RunQueued, sessionID, runID, map[string]any{"run_id": runID, "position": position}))
	return SubmitResult{RunID: runID, Queued: true, Position: position}, nil
}
func (s *Scheduler) startLocked(entry queuedRun) {
	if entry.userMessage.ID != "" {
		s.runner.AppendUser(entry.s, entry.userMessage)
		entry.userMessage = events.Message{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	active := &activeRun{cancel: cancel, runID: entry.runID, done: make(chan struct{})}
	s.active[entry.s.ID] = active
	entry.s.SetRun(session.RunState{Status: "running", RunID: entry.runID, MaxTurns: s.cfg().Run.MaxTurns})
	s.bus.Publish(events.New(events.RunStarted, entry.s.ID, entry.runID, map[string]any{"run_id": entry.runID, "user_message_id": entry.userMessageID}))
	go func() {
		reason, detail, turns := s.runner.Run(ctx, entry.s, entry.runID)
		s.finish(entry, reason, detail, turns)
		close(active.done)
	}()
}
func (s *Scheduler) finish(entry queuedRun, reason, detail string, turns int) {
	s.mu.Lock()
	active := s.active[entry.s.ID]
	if active == nil || (active.runID != "" && active.runID != entry.runID) {
		s.mu.Unlock()
		return
	}
	delete(s.active, entry.s.ID)
	if active != nil && active.stopReason != "" {
		reason = active.stopReason
	}
	if reason == "model_unreachable" {
		s.unreachable[entry.s.ID] = true
	}
	if waiting := s.pending[entry.s.ID]; len(waiting) > 0 && !s.held[entry.s.ID] && !s.unreachable[entry.s.ID] {
		next := waiting[0]
		s.pending[entry.s.ID] = waiting[1:]
		entry.s.SetQueuedMessages(len(waiting) - 1)
		next.runID = fmt.Sprintf("r%d", s.ids.Add(1))
		next.s.SetRun(session.RunState{Status: "queued", RunID: next.runID, MaxTurns: s.cfg().Run.MaxTurns})
		s.queue = append(s.queue, next)
	}
	state := session.RunState{Status: "idle", MaxTurns: s.cfg().Run.MaxTurns, LastStopReason: reason}
	queueHeld := (s.held[entry.s.ID] || s.unreachable[entry.s.ID]) && len(s.pending[entry.s.ID]) > 0
	if queueHeld {
		state.Status = "held"
		state.QueuePosition = len(s.pending[entry.s.ID])
	}
	entry.s.SetRun(state)
	s.bus.Publish(events.New(events.RunStopped, entry.s.ID, entry.runID, map[string]any{"run_id": entry.runID, "reason": reason, "detail": detail, "turns": turns, "queue_held": queueHeld}))
	s.notifyAgentIdleLocked(entry.s.Snapshot().AgentID)
	for len(s.queue) > 0 && len(s.active) < s.cfg().Run.MaxConcurrent {
		next := s.queue[0]
		s.queue = s.queue[1:]
		s.startLocked(next)
	}
	s.repositionLocked()
	s.mu.Unlock()
}

func identicalPendingPosition(waiting []queuedRun, text string) int {
	for index, entry := range waiting {
		if entry.userMessage.Content == text {
			return index + 1
		}
	}
	return 0
}

// ReleaseModel releases only model-unreachable holds after a successful probe.
// A manual Stop hold remains in force until the operator explicitly sends again.
func (s *Scheduler) ReleaseModel(profileID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sessionID, held := range s.unreachable {
		if !held {
			continue
		}
		item, ok := s.registry.Get(sessionID)
		if !ok || item.ServerID != profileID {
			continue
		}
		delete(s.unreachable, sessionID)
		s.bus.Publish(events.New(events.ModelReachable, sessionID, "", map[string]any{"server_id": profileID}))
		waiting := s.pending[sessionID]
		if len(waiting) == 0 || s.held[sessionID] || s.active[sessionID] != nil {
			continue
		}
		next := waiting[0]
		s.pending[sessionID] = waiting[1:]
		item.SetQueuedMessages(len(waiting) - 1)
		next.runID = fmt.Sprintf("r%d", s.ids.Add(1))
		if len(s.active) < s.cfg().Run.MaxConcurrent {
			s.startLocked(next)
		} else {
			s.queue = append(s.queue, next)
			item.SetRun(session.RunState{Status: "queued", RunID: next.runID, MaxTurns: s.cfg().Run.MaxTurns, QueuePosition: len(s.queue)})
			s.bus.Publish(events.New(events.RunQueued, sessionID, next.runID, map[string]any{"run_id": next.runID, "position": len(s.queue)}))
		}
	}
}
func (s *Scheduler) repositionLocked() {
	for index, entry := range s.queue {
		state := entry.s.Snapshot().Run
		state.QueuePosition = index + 1
		entry.s.SetRun(state)
	}
}
func (s *Scheduler) Stop(sessionID string, all bool) []string {
	s.mu.Lock()
	stopped := []string{}
	idleAgents := map[string]bool{}
	type waitRun struct {
		id     string
		active *activeRun
		item   *session.Session
		reason string
	}
	waits := []waitRun{}
	for id, active := range s.active {
		if all || id == sessionID {
			reason := s.runner.abortReason(id, active.runID)
			active.stopReason = reason
			if len(s.pending[id]) > 0 {
				s.held[id] = true
			}
			item, _ := s.registry.Get(id)
			if item != nil {
				state := item.Snapshot().Run
				state.Status = "stopping"
				item.SetRun(state)
			}
			s.bus.Publish(events.New(events.RunStopping, id, itemRunID(item), map[string]any{"reason": reason}))
			active.cancel()
			stopped = append(stopped, id)
			waits = append(waits, waitRun{id: id, active: active, item: item, reason: reason})
		}
	}
	kept := s.queue[:0]
	for _, entry := range s.queue {
		if all || entry.s.ID == sessionID {
			idleAgents[entry.s.Snapshot().AgentID] = true
			if len(s.pending[entry.s.ID]) > 0 {
				s.held[entry.s.ID] = true
			}
			s.bus.Publish(events.New(events.RunStopping, entry.s.ID, entry.runID, map[string]any{"reason": "done"}))
			status := "idle"
			if s.held[entry.s.ID] {
				status = "held"
			}
			entry.s.SetRun(session.RunState{Status: status, MaxTurns: s.cfg().Run.MaxTurns, QueuePosition: len(s.pending[entry.s.ID]), LastStopReason: "done"})
			s.bus.Publish(events.New(events.RunStopped, entry.s.ID, entry.runID, map[string]any{"run_id": entry.runID, "reason": "done", "detail": "stopped before dispatch", "turns": 0, "queue_held": s.held[entry.s.ID]}))
			stopped = append(stopped, entry.s.ID)
		} else {
			kept = append(kept, entry)
		}
	}
	s.queue = kept
	s.repositionLocked()
	for agentID := range idleAgents {
		s.notifyAgentIdleLocked(agentID)
	}
	s.mu.Unlock()
	for _, waiting := range waits {
		timer := time.NewTimer(cancellationBound)
		select {
		case <-waiting.active.done:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			detail := abortDetail(true)
			s.runner.recordAbort(waiting.item, waiting.active.runID, waiting.reason, detail)
			s.forceFinish(waiting.id, waiting.active, detail)
		}
	}
	return stopped
}

func (s *Scheduler) forceFinish(sessionID string, expected *activeRun, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	active := s.active[sessionID]
	if active != expected {
		return
	}
	delete(s.active, sessionID)
	item, _ := s.registry.Get(sessionID)
	if item == nil {
		return
	}
	turn := item.Snapshot().Run.Turn
	state := session.RunState{Status: "idle", MaxTurns: s.cfg().Run.MaxTurns, LastStopReason: active.stopReason}
	queueHeld := (s.held[sessionID] || s.unreachable[sessionID]) && len(s.pending[sessionID]) > 0
	if queueHeld {
		state.Status, state.QueuePosition = "held", len(s.pending[sessionID])
	}
	item.SetRun(state)
	s.bus.Publish(events.New(events.RunStopped, sessionID, active.runID, map[string]any{"run_id": active.runID, "reason": active.stopReason, "detail": detail, "turns": turn, "queue_held": queueHeld}))
	s.notifyAgentIdleLocked(item.Snapshot().AgentID)
	for len(s.queue) > 0 && len(s.active) < s.cfg().Run.MaxConcurrent {
		next := s.queue[0]
		s.queue = s.queue[1:]
		s.startLocked(next)
	}
	s.repositionLocked()
}

func itemRunID(item *session.Session) string {
	if item == nil {
		return ""
	}
	return item.Snapshot().Run.RunID
}
func (s *Scheduler) Active(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.active[sessionID]
	if ok {
		return true
	}
	for _, q := range s.queue {
		if q.s.ID == sessionID {
			return true
		}
	}
	return false
}
