package session

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	workspaceinfo "harness/internal/workspace"
)

type RunState struct {
	Status         string `json:"status"`
	RunID          string `json:"run_id"`
	Turn           int    `json:"turn"`
	MaxTurns       int    `json:"max_turns"`
	QueuePosition  int    `json:"queue_position"`
	Partial        string `json:"partial"`
	LastStopReason string `json:"last_stop_reason"`
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
	CreatedAt            string                     `json:"created_at"`
	Closed               bool                       `json:"closed"`
	NamePinned           bool                       `json:"name_pinned"`
	Workspace            string                     `json:"workspace"`
	WorkspaceDir         string                     `json:"workspace_dir"`
	WorkspaceMissing     bool                       `json:"workspace_missing"`
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
	LogPath              string                     `json:"log_path"`
	ModelTurns           int                        `json:"model_turns"`
	CompactionCount      int                        `json:"compaction_count"`
	CompactionTokenDelta int                        `json:"compaction_token_delta"`
	CompactionModelCalls int                        `json:"compaction_model_calls"`
	CompactionPrompt     int                        `json:"compaction_prompt_tokens"`
	CompactionCompletion int                        `json:"compaction_completion_tokens"`
}
type Session struct {
	ID, Label, AgentID, ServerID, Workspace string
	WorkspaceMissing                        bool
	ProjectBlock                            string
	ProjectFiles                            []string
	ProjectNotes                            []string
	PendingRepoPolicy                       *workspaceinfo.PolicyState
	RepoPolicy                              *workspaceinfo.PolicyState
	ProjectTouch                            func(string)
	AgentName, BProfile                     string
	Closed                                  bool
	NamePinned                              bool
	Messages                                []events.Message
	Budget                                  events.Budget
	Run                                     RunState
	ToolsEnabled                            map[string]bool
	ToolCalls                               map[string]int
	LastSeen                                map[string]time.Time
	CreatedAt                               time.Time
	LogPath                                 string
	Runnable                                bool
	NotRunnableReason                       string
	MemoryBlock                             string
	MemoryPath                              string
	AgentMemoryBlock                        string
	AgentMemoryPath                         string
	PromptAddendum                          string
	SchemaTokens                            map[string]int
	MarginalTokens                          map[string]int
	queuedMessages                          int
	modelTurns                              int
	compactionCount                         int
	compactionTokenDelta                    int
	compactionModelCalls                    int
	compactionPrompt                        int
	compactionCompletion                    int
	submitting                              int
	mu                                      sync.Mutex
}

func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	tools := make([]ToolState, 0, len(s.ToolsEnabled))
	for _, name := range []string{"read_file", "list_dir", "write_file", "edit_file", "search_text", "shell", "remember", "recall", "fetch_url", "find_files", "run_script", "call_service"} {
		enabled, ok := s.ToolsEnabled[name]
		if ok {
			tools = append(tools, ToolState{Name: name, Enabled: enabled, Calls: s.ToolCalls[name], SchemaTokens: s.SchemaTokens[name], MarginalTokens: s.MarginalTokens[name]})
		}
	}
	return Snapshot{ID: s.ID, Label: s.Label, AgentID: s.AgentID, ServerID: s.ServerID, AgentName: s.AgentName, BProfile: s.BProfile, CreatedAt: s.CreatedAt.Format(time.RFC3339Nano), Closed: s.Closed, NamePinned: s.NamePinned, Workspace: s.Workspace, WorkspaceDir: s.Workspace, WorkspaceMissing: s.WorkspaceMissing, ProjectContent: s.ProjectBlock, ProjectFiles: append([]string(nil), s.ProjectFiles...), ProjectNotes: append([]string(nil), s.ProjectNotes...), PendingRepoPolicy: clonePolicyState(s.PendingRepoPolicy), RepoPolicy: clonePolicyState(s.RepoPolicy), Run: s.Run, Tools: tools, Messages: append([]events.Message{}, s.Messages...), Budget: s.Budget, QueuedMessages: s.queuedMessages, Runnable: s.Runnable, NotRunnableReason: s.NotRunnableReason, MemoryPath: s.MemoryPath, MemoryContent: s.MemoryBlock, AgentMemoryPath: s.AgentMemoryPath, AgentMemoryContent: s.AgentMemoryBlock, PromptAddendum: s.PromptAddendum, LogPath: s.LogPath, ModelTurns: s.modelTurns, CompactionCount: s.compactionCount, CompactionTokenDelta: s.compactionTokenDelta, CompactionModelCalls: s.compactionModelCalls, CompactionPrompt: s.compactionPrompt, CompactionCompletion: s.compactionCompletion}
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
	if s.RepoPolicy != nil && len(s.RepoPolicy.Policy.DefaultToolset) > 0 {
		policy := map[string]bool{}
		for _, name := range s.RepoPolicy.Policy.DefaultToolset {
			policy[name] = true
		}
		for name, value := range enabled {
			enabled[name] = value && policy[name]
		}
	}
	changed := s.AgentID != agentID || s.ServerID != agent.B || s.AgentName != agent.Name || s.BProfile != profile.Label || s.PromptAddendum != agent.PromptAddendum
	if !changed {
		for name, value := range enabled {
			if s.ToolsEnabled[name] != value {
				changed = true
				break
			}
		}
	}
	s.AgentID, s.ServerID, s.AgentName, s.BProfile = agentID, agent.B, agent.Name, profile.Label
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
