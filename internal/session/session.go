package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	workspaceinfo "harness/internal/workspace"
)

type RunState struct {
	Status         string   `json:"status"`
	RunID          string   `json:"run_id"`
	Turn           int      `json:"turn"`
	MaxTurns       int      `json:"max_turns"`
	QueuePosition  int      `json:"queue_position"`
	Partial        string   `json:"partial"`
	LastStopReason string   `json:"last_stop_reason"`
	LastStopDetail string   `json:"last_stop_detail,omitempty"`
	LastRunID      string   `json:"last_run_id,omitempty"`
	ArmedDetectors []string `json:"armed_detectors,omitempty"`
	ResultLabel    string   `json:"result_label,omitempty"`
}
type ToolState struct {
	Name           string `json:"name"`
	Enabled        bool   `json:"enabled"`
	Calls          int    `json:"calls"`
	SchemaTokens   int    `json:"schema_tokens"`
	MarginalTokens int    `json:"marginal_tokens"`
}
type Snapshot struct {
	ID                   string                     `json:"id"`
	Label                string                     `json:"label"`
	AgentID              string                     `json:"agent_id"`
	ServerID             string                     `json:"server_id"`
	AgentName            string                     `json:"agent_name"`
	BProfile             string                     `json:"b_profile"`
	Role                 string                     `json:"role"`
	PlanID               string                     `json:"plan_id,omitempty"`
	PlanName             string                     `json:"plan_name,omitempty"`
	PlanDir              string                     `json:"plan_dir,omitempty"`
	PlanRepo             string                     `json:"plan_repo,omitempty"`
	CreatedAt            string                     `json:"created_at"`
	Closed               bool                       `json:"closed"`
	NamePinned           bool                       `json:"name_pinned"`
	Workspace            string                     `json:"workspace"`
	WorkspaceDir         string                     `json:"workspace_dir"`
	WorkspaceMissing     bool                       `json:"workspace_missing"`
	Scratch              bool                       `json:"scratch,omitempty"`
	ProjectContent       string                     `json:"project_content"`
	ProjectFiles         []string                   `json:"project_files"`
	ProjectNotes         []string                   `json:"project_notes"`
	PendingRepoPolicy    *workspaceinfo.PolicyState `json:"pending_repo_policy,omitempty"`
	RepoPolicy           *workspaceinfo.PolicyState `json:"repo_policy,omitempty"`
	Run                  RunState                   `json:"run"`
	Tools                []ToolState                `json:"tools"`
	Messages             []events.Message           `json:"messages"`
	Budget               events.Budget              `json:"budget"`
	QueuedMessages       int                        `json:"queued_messages"`
	Runnable             bool                       `json:"runnable"`
	NotRunnableReason    string                     `json:"not_runnable_reason"`
	MemoryPath           string                     `json:"memory_path"`
	MemoryContent        string                     `json:"memory_content"`
	AgentMemoryPath      string                     `json:"agent_memory_path"`
	AgentMemoryContent   string                     `json:"agent_memory_content"`
	PromptAddendum       string                     `json:"-"`
	NetworkBoundary      string                     `json:"network_boundary"`
	LogPath              string                     `json:"log_path"`
	ModelTurns           int                        `json:"model_turns"`
	CompactionCount      int                        `json:"compaction_count"`
	CompactionTokenDelta int                        `json:"compaction_token_delta"`
	CompactionModelCalls int                        `json:"compaction_model_calls"`
	CompactionPrompt     int                        `json:"compaction_prompt_tokens"`
	CompactionCompletion int                        `json:"compaction_completion_tokens"`
}
type Session struct {
	ID, Label, AgentID, ServerID, Workspace              string
	Role, PlanID, PlanName, PlanDir, PlanRepo, PlansRoot string
	WorkspaceMissing                                     bool
	Scratch                                              bool
	ProjectBlock                                         string
	ProjectFiles                                         []string
	ProjectNotes                                         []string
	PendingRepoPolicy                                    *workspaceinfo.PolicyState
	RepoPolicy                                           *workspaceinfo.PolicyState
	PlanRepos                                            func() []string
	RegisterPlan                                         func(string) (Plan, bool, error)
	ProjectTouch                                         func(string)
	EnsurePlan                                           func(string)
	AgentName, BProfile                                  string
	Closed                                               bool
	NamePinned                                           bool
	Messages                                             []events.Message
	Budget                                               events.Budget
	Run                                                  RunState
	ToolsEnabled                                         map[string]bool
	ToolCalls                                            map[string]int
	LastSeen                                             map[string]time.Time
	TouchedPlanRepos                                     map[string]bool
	CreatedAt                                            time.Time
	LogPath                                              string
	Runnable                                             bool
	NotRunnableReason                                    string
	MemoryBlock                                          string
	MemoryPath                                           string
	AgentMemoryBlock                                     string
	AgentMemoryPath                                      string
	PromptAddendum                                       string
	NetworkBoundary                                      string
	SchemaTokens                                         map[string]int
	MarginalTokens                                       map[string]int
	queuedMessages                                       int
	modelTurns                                           int
	compactionCount                                      int
	compactionTokenDelta                                 int
	compactionModelCalls                                 int
	compactionPrompt                                     int
	compactionCompletion                                 int
	submitting                                           int
	planPage                                             bool
	planAccept                                           bool
	mu                                                   sync.Mutex
}

func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.SnapshotUnlocked()
}

// SnapshotUnlocked is for registry mutations that already hold the session lock.
func (s *Session) SnapshotUnlocked() Snapshot {
	tools := make([]ToolState, 0, len(s.ToolsEnabled))
	for _, name := range []string{"read_file", "list_dir", "write_file", "edit_file", "search_text", "shell", "remember", "recall", "fetch_url", "find_files", "run_script", "call_service"} {
		enabled, ok := s.ToolsEnabled[name]
		if ok {
			tools = append(tools, ToolState{Name: name, Enabled: enabled, Calls: s.ToolCalls[name], SchemaTokens: s.SchemaTokens[name], MarginalTokens: s.MarginalTokens[name]})
		}
	}
	return Snapshot{ID: s.ID, Label: s.Label, AgentID: s.AgentID, ServerID: s.ServerID, AgentName: s.AgentName, BProfile: s.BProfile, Role: s.Role, PlanID: s.PlanID, PlanName: s.PlanName, PlanDir: s.PlanDir, PlanRepo: s.PlanRepo, CreatedAt: s.CreatedAt.Format(time.RFC3339Nano), Closed: s.Closed, NamePinned: s.NamePinned, Workspace: s.Workspace, WorkspaceDir: s.Workspace, WorkspaceMissing: s.WorkspaceMissing, Scratch: s.Scratch, ProjectContent: s.ProjectBlock, ProjectFiles: append([]string(nil), s.ProjectFiles...), ProjectNotes: append([]string(nil), s.ProjectNotes...), PendingRepoPolicy: clonePolicyState(s.PendingRepoPolicy), RepoPolicy: clonePolicyState(s.RepoPolicy), Run: s.Run, Tools: tools, Messages: append([]events.Message{}, s.Messages...), Budget: s.Budget, QueuedMessages: s.queuedMessages, Runnable: s.Runnable, NotRunnableReason: s.NotRunnableReason, MemoryPath: s.MemoryPath, MemoryContent: s.MemoryBlock, AgentMemoryPath: s.AgentMemoryPath, AgentMemoryContent: s.AgentMemoryBlock, PromptAddendum: s.PromptAddendum, NetworkBoundary: s.NetworkBoundary, LogPath: s.LogPath, ModelTurns: s.modelTurns, CompactionCount: s.compactionCount, CompactionTokenDelta: s.compactionTokenDelta, CompactionModelCalls: s.compactionModelCalls, CompactionPrompt: s.compactionPrompt, CompactionCompletion: s.compactionCompletion}
}

func (s *Session) ReadRoot(path string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := filepath.FromSlash(path)
	if filepath.IsAbs(candidate) && s.PlansRoot != "" && pathWithin(s.PlansRoot, candidate) {
		return s.PlansRoot, nil
	}
	if s.Role == "d" && s.PlanDir != "" {
		if err := s.validatePlanDirLocked(); err != nil {
			return "", err
		}
		if !filepath.IsAbs(candidate) || pathWithin(s.PlanDir, candidate) {
			return s.PlanDir, nil
		}
		if s.PlanRepo != "" && pathWithin(s.PlanRepo, candidate) {
			s.touchPlanRepoLocked(s.PlanRepo)
			return s.PlanRepo, nil
		}
	}
	if s.Role == "b" && filepath.IsAbs(candidate) {
		if root := s.planRepoRootLocked(candidate); root != "" {
			s.touchPlanRepoLocked(root)
			return root, nil
		}
	}
	return s.Workspace, nil
}

func (s *Session) WriteRoot(path string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := filepath.FromSlash(path)
	if filepath.IsAbs(candidate) && s.PlansRoot != "" && pathWithin(s.PlansRoot, candidate) && (s.Role != "d" || s.PlanDir == "" || !pathWithin(s.PlanDir, candidate)) {
		return "", fmt.Errorf("path is outside the folder")
	}
	if s.Role != "d" {
		if filepath.IsAbs(candidate) {
			if root := s.planRepoRootLocked(candidate); root != "" {
				s.touchPlanRepoLocked(root)
				return root, nil
			}
		}
		return s.Workspace, nil
	}
	if s.PlanDir != "" {
		if err := s.validatePlanDirLocked(); err != nil {
			return "", err
		}
		if filepath.IsAbs(candidate) && !pathWithin(s.PlanDir, candidate) {
			return "", fmt.Errorf("path is outside the plan")
		}
		return s.PlanDir, nil
	}
	if filepath.IsAbs(candidate) {
		return "", fmt.Errorf("path is outside the plan")
	}
	cleaned := filepath.Clean(filepath.FromSlash(path))
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path is outside the folder")
	}
	if s.PlansRoot == "" {
		return "", fmt.Errorf("plan storage is unavailable")
	}
	if err := os.MkdirAll(s.PlansRoot, 0o700); err != nil {
		return "", err
	}
	planID, planDir, err := allocatePlanDir(s.PlansRoot)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(planDir, "plan", "items"), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(planDir, "plan.md"), []byte("# Untitled plan\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(planDir, "NOTES.md"), nil, 0o600); err != nil {
		return "", err
	}
	s.PlanID, s.PlanName, s.PlanDir = planID, "Untitled plan", planDir
	return s.PlanDir, nil
}

func (s *Session) planRepoRootLocked(candidate string) string {
	if s.PlanRepos == nil {
		return ""
	}
	best := ""
	for _, root := range s.PlanRepos() {
		root = filepath.Clean(root)
		if root != "." && pathWithin(root, candidate) && len(root) > len(best) {
			best = root
		}
	}
	return best
}

func (s *Session) touchPlanRepoLocked(root string) {
	if s.TouchedPlanRepos == nil {
		s.TouchedPlanRepos = map[string]bool{}
	}
	s.TouchedPlanRepos[filepath.Clean(root)] = true
}

func (s *Session) ResetRunTouches() {
	s.mu.Lock()
	s.TouchedPlanRepos = map[string]bool{}
	s.mu.Unlock()
}

func (s *Session) SandboxMounts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	mounts := []string{filepath.Clean(s.Workspace)}
	for root := range s.TouchedPlanRepos {
		if !pathWithin(s.Workspace, root) && !pathWithin(root, s.Workspace) {
			mounts = append(mounts, root)
		}
	}
	sort.Strings(mounts[1:])
	return mounts
}

func (s *Session) validatePlanDirLocked() error {
	if s.PlansRoot == "" || !pathWithin(s.PlansRoot, s.PlanDir) {
		return fmt.Errorf("plan storage is unavailable")
	}
	info, err := os.Lstat(s.PlanDir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("plan folder is not a directory")
	}
	return nil
}

func (s *Session) RefreshPlanName() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Role != "d" || s.PlanDir == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(s.PlanDir, "plan.md"))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			if name := strings.TrimSpace(strings.TrimLeft(trimmed, "#")); name != "" {
				s.PlanName = name
			}
			return
		}
	}
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, filepath.Clean(candidate))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func (s *Session) CombinedMemory() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	parts := []string{}
	if s.MemoryBlock != "" {
		parts = append(parts, s.MemoryBlock)
	}
	if s.AgentMemoryBlock != "" {
		parts = append(parts, s.AgentMemoryBlock)
	}
	return strings.Join(parts, "\n\n")
}
func clonePolicyState(value *workspaceinfo.PolicyState) *workspaceinfo.PolicyState {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func (s *Session) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Run.Status == "running" || s.Run.Status == "queued" || s.Run.Status == "paused" || s.Run.Status == "stopping" || s.Run.Status == "held"
}
func (s *Session) IsClosed() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.Closed }
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Closed {
		return fmt.Errorf("session is already closed")
	}
	if s.Run.Status == "running" || s.Run.Status == "queued" || s.Run.Status == "paused" || s.Run.Status == "stopping" || s.Run.Status == "held" {
		return fmt.Errorf("session is running; stop the run before closing")
	}
	if s.submitting > 0 {
		return fmt.Errorf("session is running; stop the run before closing")
	}
	s.Closed = true
	return nil
}
func (s *Session) BeginSubmission() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Closed {
		return false
	}
	s.submitting++
	return true
}
func (s *Session) EndSubmission() { s.mu.Lock(); s.submitting--; s.mu.Unlock() }
func (s *Session) Touch(path string) {
	s.mu.Lock()
	s.LastSeen[path] = time.Now().UTC()
	hook := s.ProjectTouch
	s.mu.Unlock()
	if hook != nil {
		hook(path)
	}
}
func (s *Session) TouchProject(path string) {
	s.mu.Lock()
	hook := s.ProjectTouch
	s.mu.Unlock()
	if hook != nil {
		hook(path)
	}
}
func (s *Session) AppendProject(block string, files, notes []string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	newFiles := []string{}
	for _, file := range files {
		seen := false
		for _, prior := range s.ProjectFiles {
			if prior == file {
				seen = true
				break
			}
		}
		if seen {
			continue
		}
		newFiles = append(newFiles, file)
	}
	if len(newFiles) == 0 {
		return false
	}
	if block != "" {
		if s.ProjectBlock != "" {
			s.ProjectBlock += "\n\n"
		}
		s.ProjectBlock += block
	}
	s.ProjectFiles = append(s.ProjectFiles, newFiles...)
	s.ProjectNotes = append(s.ProjectNotes, notes...)
	return true
}
func (s *Session) Policy() workspaceinfo.Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.RepoPolicy == nil {
		return workspaceinfo.Policy{}
	}
	return s.RepoPolicy.Policy
}
func (s *Session) SetRepoPolicy(value *workspaceinfo.PolicyState) {
	s.mu.Lock()
	s.RepoPolicy = value
	s.PendingRepoPolicy = nil
	s.mu.Unlock()
}
func (s *Session) LastSeenAt(path string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.LastSeen[path]
	return value, ok
}
func (s *Session) ToolEnabled(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ToolsEnabled[name]
}
func (s *Session) ToggleTool(name string, enabled bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Closed {
		return false
	}
	if _, ok := s.ToolsEnabled[name]; !ok {
		return false
	}
	if s.Role == "d" && enabled && (name == "shell" || name == "run_script") {
		return false
	}
	s.ToolsEnabled[name] = enabled
	return true
}
func (s *Session) IncrementToolCall(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ToolsEnabled[name]; ok {
		if s.ToolCalls == nil {
			s.ToolCalls = map[string]int{}
		}
		s.ToolCalls[name]++
	}
}
func (s *Session) EnabledTools() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for k, v := range s.ToolsEnabled {
		out[k] = v
	}
	return out
}
func (s *Session) ApplyAgentConfig(agentID string, agent config.Agent, profile config.Profile) bool {
	enabled := map[string]bool{}
	for _, name := range config.FullToolset() {
		enabled[name] = false
	}
	for _, name := range agent.Toolset {
		enabled[name] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profileID := agent.B
	if s.Role == "d" {
		profileID = agent.D
		enabled["shell"] = false
		enabled["run_script"] = false
	}
	if s.RepoPolicy != nil && len(s.RepoPolicy.Policy.DefaultToolset) > 0 {
		policy := map[string]bool{}
		for _, name := range s.RepoPolicy.Policy.DefaultToolset {
			policy[name] = true
		}
		for name, value := range enabled {
			enabled[name] = value && policy[name]
		}
	}
	changed := s.AgentID != agentID || s.ServerID != profileID || s.AgentName != agent.Name || s.BProfile != profile.Label || s.PromptAddendum != agent.PromptAddendum
	if !changed {
		for name, value := range enabled {
			if s.ToolsEnabled[name] != value {
				changed = true
				break
			}
		}
	}
	s.AgentID, s.ServerID, s.AgentName, s.BProfile = agentID, profileID, agent.Name, profile.Label
	s.PromptAddendum, s.ToolsEnabled = agent.PromptAddendum, enabled
	return changed
}
func (s *Session) Append(message events.Message) {
	s.mu.Lock()
	s.Messages = append(s.Messages, message)
	s.mu.Unlock()
}
func (s *Session) SetRun(state RunState)          { s.mu.Lock(); s.Run = state; s.mu.Unlock() }
func (s *Session) UpdatePartial(partial string)   { s.mu.Lock(); s.Run.Partial = partial; s.mu.Unlock() }
func (s *Session) SetBudget(budget events.Budget) { s.mu.Lock(); s.Budget = budget; s.mu.Unlock() }
func (s *Session) SetQueuedMessages(count int)    { s.mu.Lock(); s.queuedMessages = count; s.mu.Unlock() }
func (s *Session) RecordModelTurn()               { s.mu.Lock(); s.modelTurns++; s.mu.Unlock() }
func (s *Session) RecordCompaction(delta int) {
	s.mu.Lock()
	s.compactionCount++
	s.compactionTokenDelta += delta
	s.mu.Unlock()
}
func (s *Session) RecordCompactionModel(prompt, completion int) {
	s.mu.Lock()
	s.compactionModelCalls++
	s.compactionPrompt += prompt
	s.compactionCompletion += completion
	s.mu.Unlock()
}
func (s *Session) SetToolTokens(schema, marginal map[string]int) {
	s.mu.Lock()
	s.SchemaTokens = schema
	s.MarginalTokens = marginal
	s.mu.Unlock()
}
func (s *Session) SetRunnable(ok bool, reason string) {
	s.mu.Lock()
	s.Runnable, s.NotRunnableReason = ok, reason
	s.mu.Unlock()
}
func (s *Session) MessagesCopy() []events.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]events.Message(nil), s.Messages...)
}
func (s *Session) ReplaceMessages(messages []events.Message) {
	s.mu.Lock()
	s.Messages = append([]events.Message(nil), messages...)
	s.mu.Unlock()
}

// SetPlanPage marks a chat as using the operator-governed Plan surface. It is
// deliberately session-local: the browser reasserts it when the surface opens.
func (s *Session) SetPlanPage(enabled bool) {
	s.mu.Lock()
	s.planPage = enabled
	s.mu.Unlock()
}

// BeginPlanAccept opens the narrow write gate used by the Plan tray. Model
// tool calls never call this method.
func (s *Session) BeginPlanAccept() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.planPage {
		return false
	}
	s.planAccept = true
	return true
}

func (s *Session) EndPlanAccept() {
	s.mu.Lock()
	s.planAccept = false
	s.mu.Unlock()
}

func (s *Session) PlanWriteAllowed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.planPage || s.planAccept
}

func (s *Session) IsPlanPage() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.planPage
}

func (s *Session) DropLastMessage() (events.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Messages) == 0 {
		return events.Message{}, false
	}
	last := s.Messages[len(s.Messages)-1]
	s.Messages = s.Messages[:len(s.Messages)-1]
	return last, true
}

type MessageCount struct {
	Tokens    int
	Estimated bool
}

func (s *Session) SetMessageCounts(values map[string]MessageCount) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.Messages {
		if value, ok := values[s.Messages[index].ID]; ok {
			s.Messages[index].Tokens = value.Tokens
			s.Messages[index].Estimated = value.Estimated
		}
	}
}
