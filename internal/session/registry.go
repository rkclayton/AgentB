package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	workspaceinfo "harness/internal/workspace"
)

type Registry struct {
	mu          sync.Mutex
	sessions    map[string]*Session
	next        int
	profiles    func(string) (*config.Profile, bool)
	bus         *events.Bus
	writers     *events.Writers
	maxTurns    int
	config      func() config.Config
	memory      func(context.Context, string, string) (string, string, error)
	agentMemory func(context.Context, string, string) (string, string, error)
	workspaces  *workspaceinfo.Manager
	plansRoot   string
	scratchRoot string
}

func NewRegistry(bus *events.Bus, writers *events.Writers, profiles func(string) (*config.Profile, bool), maxTurns int, settings func() config.Config) *Registry {
	return &Registry{sessions: map[string]*Session{}, next: 2, profiles: profiles, bus: bus, writers: writers, maxTurns: maxTurns, config: settings}
}
func (r *Registry) SetMemoryLoader(loader func(context.Context, string, string) (string, string, error)) {
	r.memory = loader
}
func (r *Registry) SetAgentMemoryLoader(loader func(context.Context, string, string) (string, string, error)) {
	r.agentMemory = loader
}
func (r *Registry) SetWorkspaceManager(manager *workspaceinfo.Manager) { r.workspaces = manager }
func (r *Registry) SetPlansRoot(root string) {
	r.plansRoot = filepath.Clean(root)
	r.scratchRoot = filepath.Join(filepath.Dir(r.plansRoot), "scratch")
}
func (r *Registry) Create(label, agentID, workspace string) (*Session, error) {
	return r.create(label, agentID, workspace, nil, "b", "")
}
func (r *Registry) CreateRole(label, agentID, workspace, role, planID string) (*Session, error) {
	return r.create(label, agentID, workspace, nil, role, planID)
}
func (r *Registry) CreateLike(sourceID string) (*Session, error) {
	return r.createLike(sourceID)
}
func (r *Registry) createLike(sourceID string) (*Session, error) {
	source, ok := r.Get(sourceID)
	if !ok {
		return nil, fmt.Errorf("source session not found")
	}
	snapshot := source.Snapshot()
	enabled := make(map[string]bool, len(snapshot.Tools))
	for _, tool := range snapshot.Tools {
		enabled[tool.Name] = tool.Enabled
	}
	agentID := snapshot.AgentID
	if _, found := r.resolveAgent(agentID); agentID == "" || !found {
		agentID = snapshot.ServerID
	}
	return r.create("", agentID, "", enabled, "b", "")
}

// Restore rehydrates a retained chat into a fresh operational tape. The
// retained transcript is the authority; the new session.created event seeds
// this launch's discardable projector and log generation from that state.
func (r *Registry) Restore(saved Snapshot) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if saved.ID == "" {
		return nil, fmt.Errorf("restore session: missing id")
	}
	if _, exists := r.sessions[saved.ID]; exists {
		return nil, fmt.Errorf("restore session %s: duplicate id", saved.ID)
	}
	agent, ok := r.resolveAgent(saved.AgentID)
	if !ok {
		return nil, fmt.Errorf("restore session %s: unknown agent %s", saved.ID, saved.AgentID)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, saved.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("restore session %s created_at: %w", saved.ID, err)
	}
	logPath, err := r.writers.OpenSession(saved.ID)
	if err != nil {
		return nil, err
	}
	run := saved.Run
	if run.Status != "idle" {
		run.Status, run.RunID, run.QueuePosition, run.Partial = "idle", "", 0, ""
		run.LastStopReason = "aborted_mid_run"
	}
	tools, calls := map[string]bool{}, map[string]int{}
	schemaTokens, marginalTokens := map[string]int{}, map[string]int{}
	for _, tool := range saved.Tools {
		tools[tool.Name], calls[tool.Name] = tool.Enabled, tool.Calls
		schemaTokens[tool.Name], marginalTokens[tool.Name] = tool.SchemaTokens, tool.MarginalTokens
	}
	role := saved.Role
	if role == "" {
		role = "b"
	}
	planID, planDir, planRepo := "", "", ""
	if role == "d" && saved.PlanID != "" {
		planID, err = normalizePlanID(saved.PlanID)
		if err != nil {
			return nil, fmt.Errorf("restore session %s: %w", saved.ID, err)
		}
		planDir = filepath.Join(r.plansRoot, planID)
		planRepo = planRepoForRestore(planDir, saved.PlanRepo)
	}
	s := &Session{
		ID: saved.ID, Label: saved.Label, AgentID: saved.AgentID, ServerID: saved.ServerID,
		AgentName: saved.AgentName, BProfile: saved.BProfile, Role: role, PlanID: planID, PlanName: saved.PlanName, PlanDir: planDir, PlanRepo: planRepo, PlansRoot: r.plansRoot, PlanRepos: r.planRepos, RegisterPlan: r.EnsurePlan, PromptAddendum: agent.PromptAddendum, NetworkBoundary: saved.NetworkBoundary,
		Workspace: firstNonempty(saved.WorkspaceDir, saved.Workspace), WorkspaceMissing: saved.WorkspaceMissing, Scratch: saved.Scratch,
		ProjectBlock: saved.ProjectContent, ProjectFiles: append([]string(nil), saved.ProjectFiles...), ProjectNotes: append([]string(nil), saved.ProjectNotes...),
		PendingRepoPolicy: clonePolicyState(saved.PendingRepoPolicy), RepoPolicy: clonePolicyState(saved.RepoPolicy),
		Run: run, ToolsEnabled: tools, ToolCalls: calls, LastSeen: map[string]time.Time{}, CreatedAt: createdAt,
		Closed: saved.Closed, NamePinned: saved.NamePinned, Messages: append([]events.Message(nil), saved.Messages...), Budget: saved.Budget,
		LogPath: logPath, Runnable: saved.Runnable, NotRunnableReason: saved.NotRunnableReason,
		MemoryBlock: saved.MemoryContent, MemoryPath: saved.MemoryPath, AgentMemoryBlock: saved.AgentMemoryContent, AgentMemoryPath: saved.AgentMemoryPath,
		SchemaTokens: schemaTokens, MarginalTokens: marginalTokens, queuedMessages: saved.QueuedMessages,
		modelTurns: saved.ModelTurns, compactionCount: saved.CompactionCount, compactionTokenDelta: saved.CompactionTokenDelta,
		compactionModelCalls: saved.CompactionModelCalls, compactionPrompt: saved.CompactionPrompt, compactionCompletion: saved.CompactionCompletion,
	}
	if s.NetworkBoundary == "" {
		s.NetworkBoundary = NetworkBoundary(r.config())
	}
	if r.workspaces != nil && !s.WorkspaceMissing {
		s.ProjectTouch = r.projectTouch(s)
	}
	s.EnsurePlan = r.ensurePlanHook(s)
	r.sessions[s.ID] = s
	if strings.HasPrefix(s.ID, "s") {
		if value, parseErr := strconv.Atoi(strings.TrimPrefix(s.ID, "s")); parseErr == nil && value >= r.next {
			r.next = value + 1
		}
	}
	r.bus.Publish(events.New(events.SessionCreated, s.ID, "", map[string]any{"workspace_dir": s.Workspace, "session": s.SnapshotUnlocked()}))
	return s, nil
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
func planRepoForRestore(dir, saved string) string {
	if saved != "" {
		return saved
	}
	return planRepo(dir)
}
func planDisplayName(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "plan.md"))
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if name := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#")); strings.HasPrefix(strings.TrimSpace(line), "#") && name != "" {
				return name
			}
		}
	}
	return filepath.Base(dir)
}
func normalizePlanID(value string) (string, error) {
	if value == "" || value == "." || value == ".." || filepath.IsAbs(value) || filepath.Base(value) != value || strings.ContainsAny(value, `/\`) {
		return "", fmt.Errorf("plan_id: invalid plan id")
	}
	return value, nil
}
func (r *Registry) create(label, agentID, workspace string, enabled map[string]bool, role, planID string) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	agent, ok := r.resolveAgent(agentID)
	if !ok {
		return nil, fmt.Errorf("agent_id: unknown agent %s", agentID)
	}
	agentID = config.AgentID(agent.Name)
	if role == "" {
		role = "b"
	}
	if role != "b" && role != "d" {
		return nil, fmt.Errorf("role must be b or d")
	}
	profileID := agent.B
	if role == "d" {
		profileID = agent.D
		if profileID == "" {
			return nil, fmt.Errorf("agent_d is not assigned")
		}
	}
	profile, ok := r.profiles(profileID)
	if !ok {
		return nil, fmt.Errorf("agent_id: %s profile %s was not found", role, profileID)
	}
	id := "main"
	if len(r.sessions) > 0 {
		id = fmt.Sprintf("s%d", r.next)
		r.next++
	}
	autoLabel := label == ""
	if label == "" {
		label = id
	}
	planDir, planName, selectedRepo := "", "", ""
	if role == "d" && planID != "" {
		normalized, normalizeErr := normalizePlanID(planID)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		planID = normalized
		planDir = filepath.Join(r.plansRoot, planID)
		if info, statErr := os.Lstat(planDir); statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("plan_id: plan not found")
		}
		planName = planDisplayName(planDir)
		selectedRepo = planRepo(planDir)
		if workspace == "" {
			workspace = selectedRepo
		}
	}
	scratch := workspace == ""
	if scratch {
		if r.scratchRoot == "" {
			return nil, fmt.Errorf("scratch storage is unavailable")
		}
		for {
			workspace = filepath.Join(r.scratchRoot, id)
			_, statErr := os.Lstat(workspace)
			if os.IsNotExist(statErr) {
				break
			}
			if statErr != nil {
				return nil, statErr
			}
			id = fmt.Sprintf("s%d", r.next)
			r.next++
			if autoLabel {
				label = id
			}
		}
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			return nil, err
		}
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	setup := workspaceinfo.Setup{Dir: abs}
	if r.workspaces != nil {
		setup, err = r.workspaces.Inspect(abs)
		if err != nil {
			return nil, err
		}
		abs = setup.Dir
	}
	if !scratch && r.plansRoot != "" && hasAgentFile(abs) {
		if _, _, err := r.ensurePlanLocked(abs); err != nil {
			return nil, err
		}
	}
	logPath, err := r.writers.OpenSession(id)
	if err != nil {
		return nil, err
	}
	runnable, reason := runnable(profile, r.config().Context.Accounting)
	tools := enabled
	if tools == nil {
		tools = map[string]bool{}
		for _, name := range config.FullToolset() {
			tools[name] = false
		}
		for _, name := range agent.Toolset {
			tools[name] = true
		}
	}
	if role == "d" {
		tools["shell"] = false
		tools["run_script"] = false
	}
	var activePolicy, pendingPolicy *workspaceinfo.PolicyState
	if setup.Policy.Path != "" {
		copy := setup.Policy
		if setup.Policy.Approved && setup.Policy.Error == "" {
			activePolicy = &copy
		} else {
			pendingPolicy = &copy
		}
	}
	if activePolicy != nil && len(activePolicy.Policy.DefaultToolset) > 0 {
		selected := map[string]bool{}
		for name := range tools {
			selected[name] = false
		}
		for _, name := range activePolicy.Policy.DefaultToolset {
			if _, ok := selected[name]; ok {
				selected[name] = true
			}
		}
		tools = selected
	}
	memoryBlock, memoryPath := "", ""
	if r.memory != nil {
		memoryBlock, memoryPath, err = r.memory(context.Background(), abs, agent.B)
		if err != nil {
			return nil, err
		}
	}
	agentMemoryBlock, agentMemoryPath := "", ""
	if r.agentMemory != nil {
		agentMemoryBlock, agentMemoryPath, err = r.agentMemory(context.Background(), agentID, agent.B)
		if err != nil {
			return nil, err
		}
	}
	settings := r.config()
	session := &Session{ID: id, Label: label, AgentID: agentID, ServerID: profileID, AgentName: agent.Name, BProfile: profile.Label, Role: role, PlanID: planID, PlanName: planName, PlanDir: planDir, PlanRepo: selectedRepo, PlansRoot: r.plansRoot, PlanRepos: r.planRepos, RegisterPlan: r.EnsurePlan, PromptAddendum: agent.PromptAddendum, NetworkBoundary: NetworkBoundary(settings), Workspace: abs, WorkspaceMissing: setup.Missing, Scratch: scratch, ProjectBlock: setup.Instructions.Block, ProjectFiles: setup.Instructions.Files, ProjectNotes: setup.Instructions.Notes, PendingRepoPolicy: pendingPolicy, RepoPolicy: activePolicy, Run: RunState{Status: "idle", MaxTurns: r.maxTurns}, ToolsEnabled: tools, ToolCalls: map[string]int{}, LastSeen: map[string]time.Time{}, CreatedAt: time.Now().UTC(), LogPath: logPath, Runnable: runnable, NotRunnableReason: reason, MemoryBlock: memoryBlock, MemoryPath: memoryPath, AgentMemoryBlock: agentMemoryBlock, AgentMemoryPath: agentMemoryPath, SchemaTokens: map[string]int{}, MarginalTokens: map[string]int{}}
	if r.workspaces != nil && !setup.Missing {
		session.ProjectTouch = r.projectTouch(session)
	}
	session.EnsurePlan = r.ensurePlanHook(session)
	session.Messages = []events.Message{}
	session.Budget = initialBudget(profile)
	r.sessions[id] = session
	r.bus.Publish(events.New(events.SessionCreated, id, "", map[string]any{"workspace_dir": abs, "session": session.Snapshot()}))
	if len(setup.Instructions.Files) > 0 {
		r.bus.Publish(events.New(events.ProjectInstructions, id, "", map[string]any{"block": setup.Instructions.Block, "files": setup.Instructions.Files, "notes": setup.Instructions.Notes, "lazy": false}))
	}
	return session, nil
}

func NetworkBoundary(settings config.Config) string {
	reachable := "loopback and Tailscale (100.64.0.0/10)"
	fetch := "public addresses and exact tools.fetch.allow_internal_hosts entries"
	if settings.Shell.AllowLocalNetwork && len(settings.Shell.ConfirmedLocalSubnets) > 0 {
		subnets := strings.Join(settings.Shell.ConfirmedLocalSubnets, ", ")
		reachable += ", plus the operator-confirmed LAN subnets " + subnets
		fetch += ", plus the operator-confirmed LAN subnets " + subnets
	}
	return "Network boundary: service-context shell may reach " + reachable + "; fetch_url may reach " + fetch + " and always refuses link-local, cloud metadata, and this Agent_b listener; when another target is needed, offer Run as you for operator approval under the operator's non-elevated identity."
}

func hasAgentFile(dir string) bool {
	for _, name := range []string{"AGENT_B.md", "AGENTS.md", "CLAUDE.md"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func (r *Registry) ensurePlanHook(item *Session) func(string) {
	return func(dir string) {
		if item.Scratch || !hasAgentFile(dir) {
			return
		}
		_, _, _ = r.EnsurePlan(dir)
	}
}

func (r *Registry) ApplyRepoPolicySession(sessionID string, state workspaceinfo.PolicyState) error {
	s, ok := r.Get(sessionID)
	if !ok {
		return fmt.Errorf("session not found")
	}
	copy := state
	s.SetRepoPolicy(&copy)
	if len(state.Policy.DefaultToolset) > 0 {
		enabled := s.EnabledTools()
		for name := range enabled {
			s.ToggleTool(name, false)
		}
		for _, name := range state.Policy.DefaultToolset {
			s.ToggleTool(name, true)
		}
	}
	return nil
}

func (r *Registry) projectTouch(item *Session) func(string) {
	return func(relative string) {
		snapshot := item.Snapshot()
		root := snapshot.WorkspaceDir
		target := filepath.Join(root, relative)
		info, statErr := os.Stat(target)
		if statErr == nil && !info.IsDir() {
			target = filepath.Dir(target)
		}
		addition, loadErr := workspaceinfo.LoadInstructions(root, target)
		if loadErr != nil {
			r.bus.Publish(events.New(events.Error, item.ID, "", map[string]any{"where": "project_instructions", "message": loadErr.Error()}))
			return
		}
		if item.AppendProject(addition.Block, addition.Files, addition.Notes) {
			r.bus.Publish(events.New(events.ProjectInstructions, item.ID, "", map[string]any{"block": addition.Block, "files": addition.Files, "notes": addition.Notes, "lazy": true}))
		}
	}
}
func (r *Registry) DenyRepoPolicy(sessionID string) error {
	s, ok := r.Get(sessionID)
	if !ok {
		return fmt.Errorf("session not found")
	}
	s.mu.Lock()
	s.PendingRepoPolicy = nil
	s.mu.Unlock()
	return nil
}
func (r *Registry) RevokeRepoPolicy(workspace string) {
	for _, s := range r.List() {
		if strings.EqualFold(filepath.Clean(s.Workspace), filepath.Clean(workspace)) {
			s.mu.Lock()
			s.RepoPolicy = nil
			s.PendingRepoPolicy = nil
			s.mu.Unlock()
		}
	}
}
func (r *Registry) ClearWorkspaceMemory(workspace string) {
	for _, s := range r.List() {
		if strings.EqualFold(filepath.Clean(s.Workspace), filepath.Clean(workspace)) {
			s.mu.Lock()
			s.MemoryBlock = ""
			s.mu.Unlock()
		}
	}
}
func (r *Registry) ClearAgentMemory(agentID string) {
	for _, s := range r.List() {
		if s.AgentID == agentID {
			s.mu.Lock()
			s.AgentMemoryBlock = ""
			s.mu.Unlock()
		}
	}
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
func (r *Registry) AgentRunnable(agentID string) (bool, string) {
	return r.AgentRoleRunnable(agentID, "b")
}
func (r *Registry) AgentRoleRunnable(agentID, role string) (bool, string) {
	agent, ok := r.resolveAgent(agentID)
	if !ok {
		return false, "unknown agent " + agentID
	}
	profileID := agent.B
	if role == "d" {
		profileID = agent.D
		if profileID == "" {
			return false, "agent_d is not assigned"
		}
	}
	return r.ProfileRunnable(profileID)
}
func (r *Registry) resolveAgent(id string) (*config.Agent, bool) {
	cfg := r.config()
	if agent, ok := cfg.Agent(id); ok {
		return agent, true
	}
	for i := range cfg.Agents {
		if cfg.Agents[i].B == id {
			agent := cfg.Agents[i]
			return &agent, true
		}
	}
	for _, candidate := range cfg.Servers {
		if config.AgentID(candidate.Label) == id {
			return &config.Agent{Name: candidate.Label, B: candidate.ID, Toolset: config.FullToolset()}, true
		}
	}
	if profile, ok := r.profiles(id); ok {
		return &config.Agent{Name: profile.Label, B: profile.ID, Toolset: config.FullToolset()}, true
	}
	return nil, false
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
func (r *Registry) Rename(id, label string) error { return r.RenameBy(id, label, "user") }
func (r *Registry) RenameBy(id, label, by string) error {
	s, ok := r.Get(id)
	if !ok {
		return fmt.Errorf("session not found")
	}
	label = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(label, "\r", " "), "\n", " "))
	if label == "" {
		return fmt.Errorf("label is required")
	}
	if len([]rune(label)) > 80 {
		label = string([]rune(label)[:80])
	}
	if by == "aux" {
		by = "c"
	}
	if by != "user" && by != "c" {
		return fmt.Errorf("rename author is invalid")
	}
	s.mu.Lock()
	if by == "c" && s.NamePinned {
		s.mu.Unlock()
		return nil
	}
	s.Label = label
	if by == "user" {
		s.NamePinned = true
	}
	s.mu.Unlock()
	r.bus.Publish(events.New(events.SessionRenamed, id, "", map[string]any{"session_id": id, "label": label, "by": by}))
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
	s.AgentName, s.BProfile = profile.Label, profile.Label
	s.Runnable, s.NotRunnableReason = true, ""
	s.MemoryBlock, s.MemoryPath = memoryBlock, memoryPath
	s.Budget = initialBudget(profile)
	s.mu.Unlock()

	r.bus.Publish(events.New(events.SessionUpdated, id, "", map[string]any{
		"session_id":          id,
		"server_id":           serverID,
		"agent_name":          profile.Label,
		"b_profile":           profile.Label,
		"runnable":            true,
		"not_runnable_reason": "",
		"memory_path":         memoryPath,
		"memory_content":      memoryBlock,
	}))
	return nil
}

func (r *Registry) SetAgent(id, agentID string) error {
	s, ok := r.Get(id)
	if !ok {
		return fmt.Errorf("session not found")
	}
	agent, ok := r.resolveAgent(agentID)
	if !ok {
		return fmt.Errorf("agent_id: unknown agent %s", agentID)
	}
	snapshot := s.Snapshot()
	profileID := agent.B
	if snapshot.Role == "d" {
		profileID = agent.D
		if profileID == "" {
			return fmt.Errorf("agent_id: agent_d is not assigned")
		}
	}
	profile, ok := r.profiles(profileID)
	if !ok {
		return fmt.Errorf("agent_id: %s profile %s was not found", snapshot.Role, profileID)
	}
	agentID = config.AgentID(agent.Name)
	runnable, reason := runnable(profile, r.config().Context.Accounting)
	if !runnable {
		return fmt.Errorf("agent_id: %s", reason)
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
	s.mu.Unlock()
	memoryBlock, memoryPath := "", ""
	if r.memory != nil {
		var err error
		memoryBlock, memoryPath, err = r.memory(context.Background(), workspace, profileID)
		if err != nil {
			return err
		}
	}
	agentMemoryBlock, agentMemoryPath := "", ""
	if r.agentMemory != nil {
		var err error
		agentMemoryBlock, agentMemoryPath, err = r.agentMemory(context.Background(), agentID, profileID)
		if err != nil {
			return err
		}
	}
	enabled := map[string]bool{}
	for _, name := range config.FullToolset() {
		enabled[name] = false
	}
	for _, name := range agent.Toolset {
		enabled[name] = true
	}
	if snapshot.Role == "d" {
		enabled["shell"] = false
		enabled["run_script"] = false
	}
	s.mu.Lock()
	s.AgentID, s.ServerID, s.AgentName, s.BProfile = agentID, profileID, agent.Name, profile.Label
	s.PromptAddendum, s.ToolsEnabled = agent.PromptAddendum, enabled
	s.Runnable, s.NotRunnableReason = true, ""
	s.MemoryBlock, s.MemoryPath, s.AgentMemoryBlock, s.AgentMemoryPath, s.Budget = memoryBlock, memoryPath, agentMemoryBlock, agentMemoryPath, initialBudget(profile)
	s.mu.Unlock()
	r.bus.Publish(events.New(events.SessionUpdated, id, "", map[string]any{"session_id": id, "agent_id": agentID, "server_id": profileID, "agent_name": agent.Name, "b_profile": profile.Label, "runnable": true, "not_runnable_reason": "", "memory_path": memoryPath, "memory_content": memoryBlock}))
	return nil
}

func (r *Registry) ApplyAgentBinding(agentID string) error {
	agent, ok := r.resolveAgent(agentID)
	if !ok {
		return fmt.Errorf("agent_id: unknown agent %s", agentID)
	}
	for _, item := range r.List() {
		snapshot := item.Snapshot()
		if snapshot.AgentID != agentID || snapshot.Closed {
			continue
		}
		if snapshot.Run.Status == "running" || snapshot.Run.Status == "stopping" {
			return fmt.Errorf("agent_id: session %s is running", snapshot.ID)
		}
		profileID := agent.B
		if snapshot.Role == "d" {
			profileID = agent.D
		}
		profile, ok := r.profiles(profileID)
		if !ok {
			return fmt.Errorf("agent_id: %s profile %s was not found", snapshot.Role, profileID)
		}
		if runnable, reason := runnable(profile, r.config().Context.Accounting); !runnable {
			return fmt.Errorf("agent_id: %s", reason)
		}
		memoryBlock, memoryPath := snapshot.MemoryContent, snapshot.MemoryPath
		if r.memory != nil {
			var err error
			memoryBlock, memoryPath, err = r.memory(context.Background(), snapshot.Workspace, profileID)
			if err != nil {
				return err
			}
		}
		agentMemoryBlock, agentMemoryPath := snapshot.AgentMemoryContent, snapshot.AgentMemoryPath
		if r.agentMemory != nil {
			var err error
			agentMemoryBlock, agentMemoryPath, err = r.agentMemory(context.Background(), agentID, profileID)
			if err != nil {
				return err
			}
		}
		item.ApplyAgentConfig(agentID, *agent, *profile)
		item.mu.Lock()
		item.MemoryBlock, item.MemoryPath = memoryBlock, memoryPath
		item.AgentMemoryBlock, item.AgentMemoryPath = agentMemoryBlock, agentMemoryPath
		item.Budget = initialBudget(profile)
		item.mu.Unlock()
		r.bus.Publish(events.New(events.SessionUpdated, item.ID, "", map[string]any{
			"session_id": item.ID, "agent_id": agentID, "server_id": profileID,
			"agent_name": agent.Name, "b_profile": profile.Label, "runnable": true,
			"not_runnable_reason": "", "memory_path": memoryPath, "memory_content": memoryBlock,
			"agent_memory_path": agentMemoryPath, "agent_memory_content": agentMemoryBlock,
		}))
	}
	return nil
}

func (r *Registry) ApplyAgentToolset(agentID string, enabled map[string]bool) {
	for _, item := range r.List() {
		if item.AgentID != agentID {
			continue
		}
		for _, name := range config.FullToolset() {
			item.ToggleTool(name, enabled[name])
		}
	}
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
	if r.agentMemory != nil {
		block, path, loadErr := r.agentMemory(context.Background(), s.AgentID, s.ServerID)
		if loadErr != nil {
			s.mu.Unlock()
			return "", loadErr
		}
		s.AgentMemoryBlock, s.AgentMemoryPath = block, path
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

func (r *Registry) Reopen(id string) error {
	s, ok := r.Get(id)
	if !ok {
		return fmt.Errorf("session not found")
	}
	s.mu.Lock()
	if !s.Closed {
		s.mu.Unlock()
		return fmt.Errorf("session is already open")
	}
	s.Closed = false
	s.mu.Unlock()
	r.bus.Publish(events.New(events.SessionReopened, id, "", map[string]any{"session_id": id}))
	return nil
}

func (r *Registry) Delete(id string) (events.SessionInventory, error) {
	r.mu.Lock()
	s, ok := r.sessions[id]
	if !ok {
		r.mu.Unlock()
		return events.SessionInventory{}, fmt.Errorf("session not found")
	}
	if !s.IsClosed() {
		r.mu.Unlock()
		return events.SessionInventory{}, fmt.Errorf("session must be closed before deletion")
	}
	if s.IsRunning() {
		r.mu.Unlock()
		return events.SessionInventory{}, fmt.Errorf("session is running")
	}
	r.mu.Unlock()
	inventory, err := r.writers.DeleteSession(id)
	if err != nil {
		return events.SessionInventory{}, err
	}
	r.mu.Lock()
	delete(r.sessions, id)
	r.mu.Unlock()
	return inventory, nil
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
		Categories:       map[string]int{"system": 0, "project": 0, "workspace_memory": 0, "agent_memory": 0, "tools": 0, "history": 0, "files": 0, "results": 0, "fetched": 0, "summary": 0},
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
