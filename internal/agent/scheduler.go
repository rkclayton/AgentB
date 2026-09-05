package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
)

type queuedRun struct {
	s                    *session.Session
	runID, userMessageID string
	userMessage          events.Message
}
type activeRun struct{ cancel context.CancelFunc }
type SubmitResult struct {
	RunID    string `json:"run_id"`
	Queued   bool   `json:"queued,omitempty"`
	Position int    `json:"position,omitempty"`
}
type Scheduler struct {
	mu       sync.Mutex
	runner   *Runner
	registry *session.Registry
	bus      *events.Bus
	cfg      func() config.Config
	active   map[string]activeRun
	queue    []queuedRun
	pending  map[string][]queuedRun
	ids      atomic.Int64
}

func NewScheduler(runner *Runner, registry *session.Registry, bus *events.Bus, cfg func() config.Config) *Scheduler {
	return &Scheduler{runner: runner, registry: registry, bus: bus, cfg: cfg, active: map[string]activeRun{}, pending: map[string][]queuedRun{}}
}
func (s *Scheduler) Submit(ctx context.Context, sessionID, text string) (SubmitResult, error) {
	item, ok := s.registry.Get(sessionID)
	if !ok {
		return SubmitResult{}, fmt.Errorf("session not found")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, active := s.active[sessionID]; active || item.IsRunning() {
		depth := s.cfg().Run.QueueDepth
		if depth == 0 {
			return SubmitResult{}, fmt.Errorf("run in progress")
		}
		if len(s.pending[sessionID]) >= depth {
			return SubmitResult{}, fmt.Errorf("queue full")
		}
		message, err := s.runner.QueueUser(ctx, item, text)
		if err != nil {
			return SubmitResult{}, err
		}
		s.pending[sessionID] = append(s.pending[sessionID], queuedRun{s: item, userMessageID: message.ID, userMessage: message})
		position := len(s.pending[sessionID])
		item.SetQueuedMessages(position)
		s.bus.Publish(events.New(events.MessageQueued, sessionID, "", map[string]any{"message_id": message.ID, "position": position}))
		return SubmitResult{Queued: true, Position: position}, nil
	}
	message, err := s.runner.AddUser(ctx, item, text)
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
	s.active[entry.s.ID] = activeRun{cancel: cancel}
	entry.s.SetRun(session.RunState{Status: "running", RunID: entry.runID, MaxTurns: s.cfg().Run.MaxTurns})
	s.bus.Publish(events.New(events.RunStarted, entry.s.ID, entry.runID, map[string]any{"run_id": entry.runID, "user_message_id": entry.userMessageID}))
	go func() {
		reason, detail, turns := s.runner.Run(ctx, entry.s, entry.runID)
		s.finish(entry, reason, detail, turns)
	}()
}
func (s *Scheduler) finish(entry queuedRun, reason, detail string, turns int) {
	s.mu.Lock()
	delete(s.active, entry.s.ID)
	if reason == "user_stop" {
		discarded := len(s.pending[entry.s.ID])
		delete(s.pending, entry.s.ID)
		entry.s.SetQueuedMessages(0)
		if discarded > 0 {
			detail = fmt.Sprintf("discarded %d queued message(s)", discarded)
		}
	} else if waiting := s.pending[entry.s.ID]; len(waiting) > 0 {
		next := waiting[0]
		s.pending[entry.s.ID] = waiting[1:]
		entry.s.SetQueuedMessages(len(waiting) - 1)
		next.runID = fmt.Sprintf("r%d", s.ids.Add(1))
		next.s.SetRun(session.RunState{Status: "queued", RunID: next.runID, MaxTurns: s.cfg().Run.MaxTurns})
		s.queue = append(s.queue, next)
	}
	entry.s.SetRun(session.RunState{Status: "idle", MaxTurns: s.cfg().Run.MaxTurns, LastStopReason: reason})
	s.bus.Publish(events.New(events.RunStopped, entry.s.ID, entry.runID, map[string]any{"run_id": entry.runID, "reason": reason, "detail": detail, "turns": turns}))
	for len(s.queue) > 0 && len(s.active) < s.cfg().Run.MaxConcurrent {
		next := s.queue[0]
		s.queue = s.queue[1:]
		s.startLocked(next)
	}
	s.repositionLocked()
	s.mu.Unlock()
}
func (s *Scheduler) repositionLocked() {
	for index, entry := range s.queue {
		state := entry.s.Snapshot(nil).Run
		state.QueuePosition = index + 1
		entry.s.SetRun(state)
	}
}
func (s *Scheduler) Stop(sessionID string, all bool) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	stopped := []string{}
	for id, active := range s.active {
		if all || id == sessionID {
			active.cancel()
			stopped = append(stopped, id)
		}
	}
	kept := s.queue[:0]
	for _, entry := range s.queue {
		if all || entry.s.ID == sessionID {
			discarded := len(s.pending[entry.s.ID])
			delete(s.pending, entry.s.ID)
			entry.s.SetQueuedMessages(0)
			entry.s.SetRun(session.RunState{Status: "idle", MaxTurns: s.cfg().Run.MaxTurns, LastStopReason: "user_stop"})
			detail := ""
			if discarded > 0 {
				detail = fmt.Sprintf("discarded %d queued message(s)", discarded)
			}
			s.bus.Publish(events.New(events.RunStopped, entry.s.ID, entry.runID, map[string]any{"run_id": entry.runID, "reason": "user_stop", "detail": detail, "turns": 0}))
			stopped = append(stopped, entry.s.ID)
		} else {
			kept = append(kept, entry)
		}
	}
	s.queue = kept
	s.repositionLocked()
	return stopped
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
