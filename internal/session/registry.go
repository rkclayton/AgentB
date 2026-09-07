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
	return r.create(label, serverID, workspace, nil)
}
func (r *Registry) CreateLike(sourceID string) (*Session, error) {
	source, ok := r.Get(sourceID)
	if !ok {
		return nil, fmt.Errorf("source session not found")
	}
	snapshot := source.Snapshot()
	enabled := make(map[string]bool, len(snapshot.Tools))
	for _, tool := range snapshot.Tools {
		enabled[tool.Name] = tool.Enabled
	}
	return r.create("", snapshot.ServerID, snapshot.Workspace, enabled)
}
func (r *Registry) create(label, serverID, workspace string, enabled map[string]bool) (*Session, error) {
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
	tools := enabled
	if tools == nil {
		tools = map[string]bool{"read_file": true, "list_dir": true, "write_file": true, "edit_file": true, "search_text": true, "shell": true, "remember": true, "recall": true, "fetch_url": true, "find_files": true, "run_script": true, "call_service": true}
	}
	memoryBlock, memoryPath := "", ""
	if r.memory != nil {
		memoryBlock, memoryPath, err = r.memory(context.Background(), abs, serverID)
		if err != nil {
			return nil, err
		}
	}
	session := &Session{ID: id, Label: label, ServerID: serverID, AgentName: profile.Label, MainProfile: profile.Label, Workspace: abs, Run: RunState{Status: "idle", MaxTurns: r.maxTurns}, ToolsEnabled: tools, ToolCalls: map[string]int{}, LastSeen: map[string]time.Time{}, CreatedAt: time.Now().UTC(), LogPath: logPath, Runnable: runnable, NotRunnableReason: reason, MemoryBlock: memoryBlock, MemoryPath: memoryPath, SchemaTokens: map[string]int{}, MarginalTokens: map[string]int{}}
	session.Messages = []events.Message{}
	session.Budget = initialBudget(profile)
	r.sessions[id] = session
	r.bus.Publish(events.New(events.SessionCreated, id, "", map[string]any{"session": session.Snapshot()}))
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
func (r *Registry) ProfileInUse(serverID string) (string, bool) {
	for _, item := range r.List() {
		if item.ServerID == serverID {
			return item.ID, true
		}
	}
	return "", false
}
func (r *Registry) ProfileRunnable(serverID string) (bool, string) {
	profile, ok := r.profiles(serverID)
	if !ok {
		return false, "unknown profile " + serverID
	}
	return runnable(profile, r.config().Context.Accounting)
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
	if s.Closed {
		s.mu.Unlock()
		return fmt.Errorf("session is closed")
	}
	s.Label = label
	s.mu.Unlock()
	r.bus.Publish(events.New(events.SessionRenamed, id, "", map[string]any{"session_id": id, "label": label}))
	return nil
}
func (r *Registry) SetServer(id, serverID string) error {
	s, ok := r.Get(id)
	if !ok {
		return fmt.Errorf("session not found")
	}
	profile, ok := r.profiles(serverID)
	if !ok {
		return fmt.Errorf("server_id: unknown profile %s", serverID)
	}
	runnable, reason := runnable(profile, r.config().Context.Accounting)
	if !runnable {
		return fmt.Errorf("server_id: %s", reason)
	}

	s.mu.Lock()
	if s.Closed {
		s.mu.Unlock()
		return fmt.Errorf("session is closed")
	}
	if s.Run.Status != "idle" {
		s.mu.Unlock()
		return fmt.Errorf("session is running")
	}
	workspace := s.Workspace
	memoryBlock, memoryPath := s.MemoryBlock, s.MemoryPath
	s.mu.Unlock()

	if r.memory != nil {
		var err error
		memoryBlock, memoryPath, err = r.memory(context.Background(), workspace, serverID)
		if err != nil {
			return err
		}
	}

	s.mu.Lock()
	if s.Closed {
		s.mu.Unlock()
		return fmt.Errorf("session is closed")
	}
	if s.Run.Status != "idle" {
		s.mu.Unlock()
		return fmt.Errorf("session is running")
	}
	s.ServerID = serverID
	s.AgentName, s.MainProfile = profile.Label, profile.Label
	s.Runnable, s.NotRunnableReason = true, ""
	s.MemoryBlock, s.MemoryPath = memoryBlock, memoryPath
	s.Budget = initialBudget(profile)
	s.mu.Unlock()

	r.bus.Publish(events.New(events.SessionUpdated, id, "", map[string]any{
		"session_id":          id,
		"server_id":           serverID,
		"agent_name":          profile.Label,
		"main_profile":        profile.Label,
		"runnable":            true,
		"not_runnable_reason": "",
		"memory_path":         memoryPath,
		"memory_content":      memoryBlock,
	}))
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
	if s.IsClosed() {
		return "", fmt.Errorf("session is closed")
	}
	path, predecessor, err := r.writers.RotateSession(id)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.Messages = nil
	s.ToolCalls = map[string]int{}
	s.queuedMessages = 0
	s.modelTurns = 0
	s.compactionCount = 0
	s.compactionTokenDelta = 0
	s.compactionModelCalls = 0
	s.compactionPrompt = 0
	s.compactionCompletion = 0
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
	data := map[string]any{"session_id": id, "log_path": path}
	if predecessor.Offset > 0 {
		data["predecessor"] = map[string]any{"generation": predecessor.Generation, "offset": predecessor.Offset}
	}
	r.bus.Publish(events.New(events.SessionReset, id, "", data))
	return path, nil
}

func (r *Registry) DropLastMessage(id string) (events.Message, error) {
	s, ok := r.Get(id)
	if !ok {
		return events.Message{}, fmt.Errorf("session not found")
	}
	if s.IsRunning() {
		return events.Message{}, fmt.Errorf("session is running")
	}
	if s.IsClosed() {
		return events.Message{}, fmt.Errorf("session is closed")
	}
	message, ok := s.DropLastMessage()
	if !ok {
		return events.Message{}, fmt.Errorf("session has no messages")
	}
	r.bus.Publish(events.New(events.MessageRemoved, id, "", map[string]any{
		"id": message.ID, "reason": "operator_repair",
	}))
	return message, nil
}
func (r *Registry) Close(id string) error {
	s, ok := r.Get(id)
	if !ok {
		return fmt.Errorf("session not found")
	}
	if err := s.Close(); err != nil {
		return err
	}
	r.bus.Publish(events.New(events.SessionClosed, id, "", map[string]any{"session_id": id}))
	return nil
}
func runnable(profile *config.Profile, accounting string) (bool, string) {
	if reason := config.ProfileSetupReason(profile); reason != "" {
		return false, reason
	}
	c := profile.Capabilities
	n := profile.Context.NCtx
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

func initialBudget(profile *config.Profile) events.Budget {
	nctx := profile.Context.NCtx
	ceiling := nctx - profile.Context.ReserveOutput
	if ceiling < 0 {
		ceiling = 0
	}
	return events.Budget{
		NCtx: nctx, Reserve: profile.Context.ReserveOutput, Ceiling: ceiling,
		Mode: "estimated", Estimated: true, EstimatedCategories: []string{},
		Categories:       map[string]int{"system": 0, "memory": 0, "tools": 0, "history": 0, "files": 0, "results": 0, "fetched": 0, "summary": 0},
		ToolSchemaTokens: map[string]int{}, ToolMarginalTokens: map[string]int{},
	}
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
