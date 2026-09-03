package events

import "time"

type Event struct {
	Seq       int64  `json:"seq"`
	TS        string `json:"ts"`
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	Type      string `json:"type"`
	Data      any    `json:"data"`
	Body      any    `json:"body,omitempty"`
	Raw       any    `json:"raw,omitempty"`
}

const (
	Snapshot          = "snapshot"
	SessionCreated    = "session.created"
	SessionRenamed    = "session.renamed"
	SessionReset      = "session.reset"
	SessionClosed     = "session.closed"
	ServerProbed      = "server.probed"
	ConfigChanged     = "config.changed"
	Error             = "error"
	RunQueued         = "run.queued"
	RunStarted        = "run.started"
	RunStopped        = "run.stopped"
	Stage             = "stage"
	ModelRequest      = "model.request"
	ModelProgress     = "model.progress"
	ModelDelta        = "model.delta"
	ModelResponse     = "model.response"
	ToolCallEvent     = "tool.call"
	ToolResult        = "tool.result"
	ToolToggled       = "tool.toggled"
	MessageAppended   = "message.appended"
	MessageQueued     = "message.queued"
	CycleDetected     = "cycle.detected"
	ApprovalRequired  = "approval.required"
	ApprovalDecided   = "approval.decided"
	WorkspaceConflict = "workspace.conflict"
	BudgetEvent       = "budget"
)

var Stages = []string{"assemble", "call_model", "parse", "dispatch", "execute", "append", "compact", "wait_user"}
var StopReasons = []string{"done", "user_stop", "turn_ceiling", "cycle", "tool_errors", "context_ceiling", "length", "model_error", "profile_not_runnable"}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type Message struct {
	ID         string     `json:"id"`
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Reasoning  string     `json:"reasoning,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
	Category   string     `json:"category"`
	Tokens     int        `json:"tokens"`
	Estimated  bool       `json:"estimated"`
	Elided     bool       `json:"elided"`
	Turn       int        `json:"turn"`
}
type Budget struct {
	NCtx                int            `json:"n_ctx"`
	Reserve             int            `json:"reserve"`
	Ceiling             int            `json:"ceiling"`
	UsedEst             int            `json:"used_est"`
	UsedMeasured        int            `json:"used_measured"`
	Drift               int            `json:"drift"`
	CachedLast          *int           `json:"cached_last"`
	Mode                string         `json:"mode"`
	Estimated           bool           `json:"estimated"`
	EstimatedCategories []string       `json:"estimated_categories"`
	Categories          map[string]int `json:"categories"`
}

func New(eventType, sessionID, runID string, data any) Event {
	return Event{TS: time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"), SessionID: sessionID, RunID: runID, Type: eventType, Data: data}
}
