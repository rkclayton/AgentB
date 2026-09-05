package session

import (
	"sync"
	"time"

	"harness/internal/events"
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
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	Calls        int    `json:"calls"`
	SchemaTokens int    `json:"schema_tokens"`
}
type Todo struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}
type Snapshot struct {
	ID                   string           `json:"id"`
	Label                string           `json:"label"`
	ServerID             string           `json:"server_id"`
	Workspace            string           `json:"workspace"`
	Run                  RunState         `json:"run"`
	Tools                []ToolState      `json:"tools"`
	Messages             []events.Message `json:"messages"`
	Budget               events.Budget    `json:"budget"`
	Timeline             []events.Event   `json:"timeline"`
	QueuedMessages       int              `json:"queued_messages"`
	Runnable             bool             `json:"runnable"`
	NotRunnableReason    string           `json:"not_runnable_reason"`
	MemoryPath           string           `json:"memory_path"`
	MemoryContent        string           `json:"memory_content"`
	LogPath              string           `json:"log_path"`
	ModelTurns           int              `json:"model_turns"`
	CompactionCount      int              `json:"compaction_count"`
	CompactionTokenDelta int              `json:"compaction_token_delta"`
	Todos                []Todo           `json:"todos"`
}
type Session struct {
	ID, Label, ServerID, Workspace string
	Messages                       []events.Message
	Budget                         events.Budget
	Run                            RunState
	ToolsEnabled                   map[string]bool
	ToolCalls                      map[string]int
	LastSeen                       map[string]time.Time
	CreatedAt                      time.Time
	LogPath                        string
	Runnable                       bool
	NotRunnableReason              string
	MemoryBlock                    string
	MemoryPath                     string
	SchemaTokens                   map[string]int
	Todos                          []Todo
	queuedMessages                 int
	modelTurns                     int
	compactionCount                int
	compactionTokenDelta           int
	mu                             sync.Mutex
}

func (s *Session) Snapshot(timeline []events.Event) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	tools := make([]ToolState, 0, len(s.ToolsEnabled))
	for _, name := range []string{"read_file", "list_dir", "write_file", "edit_file", "search_text", "shell", "remember", "recall", "fetch_url", "find_files", "write_todos"} {
		enabled, ok := s.ToolsEnabled[name]
		if ok {
			tools = append(tools, ToolState{Name: name, Enabled: enabled, Calls: s.ToolCalls[name], SchemaTokens: s.SchemaTokens[name]})
		}
	}
	return Snapshot{ID: s.ID, Label: s.Label, ServerID: s.ServerID, Workspace: s.Workspace, Run: s.Run, Tools: tools, Messages: append([]events.Message{}, s.Messages...), Budget: s.Budget, Timeline: timeline, QueuedMessages: s.queuedMessages, Runnable: s.Runnable, NotRunnableReason: s.NotRunnableReason, MemoryPath: s.MemoryPath, MemoryContent: s.MemoryBlock, LogPath: s.LogPath, ModelTurns: s.modelTurns, CompactionCount: s.compactionCount, CompactionTokenDelta: s.compactionTokenDelta, Todos: append([]Todo{}, s.Todos...)}
}
func (s *Session) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Run.Status == "running" || s.Run.Status == "queued" || s.Run.Status == "paused" || s.Run.Status == "stopping"
}
func (s *Session) Touch(path string) { s.mu.Lock(); s.LastSeen[path] = time.Now().UTC(); s.mu.Unlock() }
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
func (s *Session) SetSchemaTokens(values map[string]int) {
	s.mu.Lock()
	s.SchemaTokens = values
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

func (s *Session) ReplaceTodos(texts []string) []Todo {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Todos = make([]Todo, len(texts))
	for index, text := range texts {
		s.Todos[index] = Todo{Text: text, Status: "pending"}
	}
	return append([]Todo(nil), s.Todos...)
}

func (s *Session) UpdateTodo(index int, status string) ([]Todo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 1 || index > len(s.Todos) {
		return append([]Todo(nil), s.Todos...), false
	}
	s.Todos[index-1].Status = status
	return append([]Todo(nil), s.Todos...), true
}
