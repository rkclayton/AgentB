package session

import (
	"sync"
	"time"

	"harness/internal/events"
)

type RunState struct {
	Status        string `json:"status"`
	RunID         string `json:"run_id"`
	Turn          int    `json:"turn"`
	MaxTurns      int    `json:"max_turns"`
	QueuePosition int    `json:"queue_position"`
	Partial       string `json:"partial"`
}
type ToolState struct {
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	Calls        int    `json:"calls"`
	SchemaTokens int    `json:"schema_tokens"`
}
type Snapshot struct {
	ID                string           `json:"id"`
	Label             string           `json:"label"`
	ServerID          string           `json:"server_id"`
	Workspace         string           `json:"workspace"`
	Run               RunState         `json:"run"`
	Tools             []ToolState      `json:"tools"`
	Messages          []events.Message `json:"messages"`
	Budget            events.Budget    `json:"budget"`
	Timeline          []events.Event   `json:"timeline"`
	QueuedMessages    int              `json:"queued_messages"`
	Runnable          bool             `json:"runnable"`
	NotRunnableReason string           `json:"not_runnable_reason"`
	MemoryPath        string           `json:"memory_path"`
}
type Session struct {
	ID, Label, ServerID, Workspace string
	Messages                       []events.Message
	Budget                         events.Budget
	Run                            RunState
	ToolsEnabled                   map[string]bool
	LastSeen                       map[string]time.Time
	CreatedAt                      time.Time
	LogPath                        string
	Runnable                       bool
	NotRunnableReason              string
	mu                             sync.Mutex
}

func (s *Session) Snapshot(timeline []events.Event) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	tools := make([]ToolState, 0, len(s.ToolsEnabled))
	for _, name := range []string{"read_file", "list_dir", "write_file", "edit_file", "grep", "shell", "remember"} {
		enabled, ok := s.ToolsEnabled[name]
		if ok {
			tools = append(tools, ToolState{Name: name, Enabled: enabled})
		}
	}
	return Snapshot{ID: s.ID, Label: s.Label, ServerID: s.ServerID, Workspace: s.Workspace, Run: s.Run, Tools: tools, Messages: append([]events.Message(nil), s.Messages...), Budget: s.Budget, Timeline: timeline, Runnable: s.Runnable, NotRunnableReason: s.NotRunnableReason}
}
func (s *Session) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Run.Status == "running" || s.Run.Status == "queued" || s.Run.Status == "paused" || s.Run.Status == "stopping"
}
