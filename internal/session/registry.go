package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"harness/internal/config"
	"harness/internal/events"
)

type Registry struct {
	mu       sync.Mutex
	sessions map[string]*Session
	next     int
	profiles func(string) (*config.Profile, bool)
	bus      *events.Bus
	writers  *events.Writers
	maxTurns int
	config   func() config.Config
	memory   func(context.Context, string, string) (string, string, error)
}

func NewRegistry(bus *events.Bus, writers *events.Writers, profiles func(string) (*config.Profile, bool), maxTurns int, settings func() config.Config) *Registry {
	return &Registry{sessions: map[string]*Session{}, next: 2, profiles: profiles, bus: bus, writers: writers, maxTurns: maxTurns, config: settings}
}
func (r *Registry) SetMemoryLoader(loader func(context.Context, string, string) (string, string, error)) {
	r.memory = loader
}
func (r *Registry) Create(label, serverID, workspace string) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	profile, ok := r.profiles(serverID)
	if !ok {
		return nil, fmt.Errorf("server_id: unknown profile %s", serverID)
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	id := "main"
	if len(r.sessions) > 0 {
		id = fmt.Sprintf("s%d", r.next)
		r.next++
	}
	if label == "" {
		label = id
	}
	logPath, err := r.writers.OpenSession(id)
	if err != nil {
		return nil, err
	}
	runnable, reason := runnable(profile, r.config().Context.Accounting)
	tools := map[string]bool{"read_file": true, "list_dir": true, "write_file": true, "edit_file": true, "grep": true, "shell": true, "remember": true}
	memoryBlock, memoryPath := "", ""
	if r.memory != nil {
		memoryBlock, memoryPath, err = r.memory(context.Background(), abs, serverID)
		if err != nil {
			return nil, err
		}
	}
	nctx := profile.Capabilities.NCtx
	if nctx == 0 {
		nctx = profile.Context.NCtxOverride
	}
	reserve := profile.Context.ReserveOutput
	session := &Session{ID: id, Label: label, ServerID: serverID, Workspace: abs, Run: RunState{Status: "idle", MaxTurns: r.maxTurns}, ToolsEnabled: tools, LastSeen: map[string]time.Time{}, CreatedAt: time.Now().UTC(), LogPath: logPath, Runnable: runnable, NotRunnableReason: reason, MemoryBlock: memoryBlock, MemoryPath: memoryPath, SchemaTokens: map[string]int{}}
	session.Messages = []events.Message{}
	ceiling := nctx - reserve
	if ceiling < 0 {
		ceiling = 0
	}
	session.Budget = events.Budget{NCtx: nctx, Reserve: reserve, Ceiling: ceiling, Mode: "estimated", Estimated: true, EstimatedCategories: []string{}, Categories: map[string]int{"system": 0, "memory": 0, "tools": 0, "history": 0, "files": 0, "results": 0, "summary": 0}}
	r.sessions[id] = session
	r.bus.Publish(events.New(events.SessionCreated, id, "", map[string]any{"session": session.Snapshot(nil)}))
	return session, nil
}
func (r *Registry) Get(id string) (*Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	return s, ok
}
func (r *Registry) Label(id string) string {
	if s, ok := r.Get(id); ok {
		return s.Label
	}
	return id
}
func (r *Registry) List() []*Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	values := make([]*Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		values = append(values, s)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return values
}
func (r *Registry) Rename(id, label string) error {
	s, ok := r.Get(id)
	if !ok {
		return fmt.Errorf("session not found")
	}
	s.mu.Lock()
	s.Label = label
	s.mu.Unlock()
	r.bus.Publish(events.New(events.SessionRenamed, id, "", map[string]any{"session_id": id, "label": label}))
	return nil
}
func (r *Registry) Reset(id string) (string, error) {
	s, ok := r.Get(id)
	if !ok {
		return "", fmt.Errorf("session not found")
	}
	if s.IsRunning() {
		return "", fmt.Errorf("session is running")
	}
	path, err := r.writers.OpenSession(id)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.Messages = nil
	s.LogPath = path
	s.Run = RunState{Status: "idle", MaxTurns: r.maxTurns}
	if r.memory != nil {
		block, memoryPath, loadErr := r.memory(context.Background(), s.Workspace, s.ServerID)
		if loadErr != nil {
			s.mu.Unlock()
			return "", loadErr
		}
		s.MemoryBlock, s.MemoryPath = block, memoryPath
	}
	s.mu.Unlock()
	r.bus.Publish(events.New(events.SessionReset, id, "", map[string]any{"session_id": id, "log_path": path}))
	return path, nil
}
func (r *Registry) Close(id string, force bool) error {
	r.mu.Lock()
	s, ok := r.sessions[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("session not found")
	}
	if s.IsRunning() && !force {
		r.mu.Unlock()
		return fmt.Errorf("session is running")
	}
	delete(r.sessions, id)
	r.mu.Unlock()
	r.bus.Publish(events.New(events.SessionClosed, id, "", map[string]any{"session_id": id}))
	return nil
}
func runnable(profile *config.Profile, accounting string) (bool, string) {
	c := profile.Capabilities
	n := c.NCtx
	if n == 0 {
		n = profile.Context.NCtxOverride
	}
	if n == 0 {
		return false, "context length unknown"
	}
	if !c.ToolCalls {
		return false, "tool calling unavailable"
	}
	if c.OverflowBehavior == "truncate" {
		return false, "server truncates context"
	}
	if !c.Streaming {
		return false, "streaming unavailable"
	}
	if accounting == "exact" && !c.Tokenize {
		return false, "exact accounting requested but this server has no /tokenize"
	}
	return true, ""
}

func (r *Registry) RefreshRunnable() {
	for _, item := range r.List() {
		profile, ok := r.profiles(item.ServerID)
		if !ok {
			item.SetRunnable(false, "profile not found")
			continue
		}
		ok, reason := runnable(profile, r.config().Context.Accounting)
		item.SetRunnable(ok, reason)
	}
}
