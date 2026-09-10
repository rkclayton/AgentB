// Package projection folds durable session events into the versioned state served to clients.
// It has no runtime dependencies: a snapshot is a pure function of a prior snapshot and a
// durably located JSONL record.
package projection

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"harness/internal/events"
	workspaceinfo "harness/internal/workspace"
)

const SchemaVersion = 1

type Cursor struct {
	Generation string `json:"generation"`
	Offset     int64  `json:"offset"`
}

type Record struct {
	Cursor Cursor       `json:"cursor"`
	Event  events.Event `json:"event"`
}

type Run struct {
	Status         string `json:"status"`
	RunID          string `json:"run_id"`
	Turn           int    `json:"turn"`
	MaxTurns       int    `json:"max_turns"`
	QueuePosition  int    `json:"queue_position"`
	Partial        string `json:"partial"`
	LastStopReason string `json:"last_stop_reason"`
}

type Tool struct {
	Name           string `json:"name"`
	Enabled        bool   `json:"enabled"`
	Calls          int    `json:"calls"`
	SchemaTokens   int    `json:"schema_tokens"`
	MarginalTokens int    `json:"marginal_tokens"`
}

type Activity struct {
	Stage            string           `json:"stage"`
	StageState       string           `json:"stage_state"`
	CompletedStages  []string         `json:"completed_stages"`
	ActiveTool       string           `json:"active_tool"`
	AlarmTool        string           `json:"alarm_tool"`
	DispatchAlarm    bool             `json:"dispatch_alarm"`
	Progress         map[string]any   `json:"progress,omitempty"`
	LastTimings      map[string]any   `json:"last_timings,omitempty"`
	Stream           *StreamTelemetry `json:"stream,omitempty"`
	CompactionSerial int              `json:"compaction_serial"`
}

type StreamTelemetry struct {
	Key             string         `json:"key"`
	StartedAt       int64          `json:"started_at"`
	LastChunkAt     int64          `json:"last_chunk_at"`
	HasChunk        bool           `json:"has_chunk"`
	ReasoningChars  int            `json:"reasoning_chars"`
	TotalChars      int            `json:"total_chars"`
	Rate            int            `json:"rate"`
	RateStartedAt   int64          `json:"rate_started_at"`
	RateChars       int            `json:"rate_chars"`
	Done            bool           `json:"done"`
	ReasoningTokens int            `json:"reasoning_tokens"`
	Timings         map[string]any `json:"timings,omitempty"`
}

type ChatEntry struct {
	Type                     string              `json:"type"`
	Key                      string              `json:"key"`
	RunID                    string              `json:"run_id,omitempty"`
	Turn                     int                 `json:"turn,omitempty"`
	Text                     string              `json:"text,omitempty"`
	Reasoning                string              `json:"reasoning,omitempty"`
	ReasoningTokens          int                 `json:"reasoningTokens,omitempty"`
	ReasoningTokensEstimated bool                `json:"reasoningTokensEstimated,omitempty"`
	ThinkingStartedMS        int64               `json:"thinkingStartedMS,omitempty"`
	ThinkingEndedMS          int64               `json:"thinkingEndedMS,omitempty"`
	ThinkingMS               *int64              `json:"thinkingMS,omitempty"`
	Done                     bool                `json:"done,omitempty"`
	ToolCallIDs              []string            `json:"toolCallIDs,omitempty"`
	CallID                   string              `json:"callID,omitempty"`
	Name                     string              `json:"name,omitempty"`
	Args                     *map[string]any     `json:"args,omitempty"`
	Result                   map[string]any      `json:"result,omitempty"`
	Content                  string              `json:"content,omitempty"`
	Event                    *events.Event       `json:"event,omitempty"`
	Decision                 string              `json:"decision,omitempty"`
	Attachments              []events.Attachment `json:"attachments,omitempty"`
	AgentRole                string              `json:"agent_role,omitempty"`
}

// Snapshot is the serializable session projection. Complete is false when the log has no
// session.created seed, so callers cannot mistake guessed defaults for reconstructed state.
type Snapshot struct {
	SchemaVersion        int                        `json:"schema_version"`
	Cursor               Cursor                     `json:"cursor"`
	Complete             bool                       `json:"complete"`
	ID                   string                     `json:"id"`
	Label                string                     `json:"label"`
	AgentID              string                     `json:"agent_id"`
	ServerID             string                     `json:"server_id"`
	AgentName            string                     `json:"agent_name"`
	BProfile             string                     `json:"b_profile"`
	CreatedAt            string                     `json:"created_at"`
	Workspace            string                     `json:"workspace"`
	WorkspaceDir         string                     `json:"workspace_dir"`
	WorkspaceMissing     bool                       `json:"workspace_missing"`
	ProjectContent       string                     `json:"project_content"`
	ProjectFiles         []string                   `json:"project_files"`
	ProjectNotes         []string                   `json:"project_notes"`
	PendingRepoPolicy    *workspaceinfo.PolicyState `json:"pending_repo_policy,omitempty"`
	RepoPolicy           *workspaceinfo.PolicyState `json:"repo_policy,omitempty"`
	Run                  Run                        `json:"run"`
	Tools                []Tool                     `json:"tools"`
	Messages             []events.Message           `json:"messages"`
	Budget               events.Budget              `json:"budget"`
	QueuedMessages       int                        `json:"queued_messages"`
	Runnable             bool                       `json:"runnable"`
	NotRunnableReason    string                     `json:"not_runnable_reason"`
	MemoryPath           string                     `json:"memory_path"`
	MemoryContent        string                     `json:"memory_content"`
	AgentMemoryPath      string                     `json:"agent_memory_path"`
	AgentMemoryContent   string                     `json:"agent_memory_content"`
	LogPath              string                     `json:"log_path"`
	ModelTurns           int                        `json:"model_turns"`
	CompactionCount      int                        `json:"compaction_count"`
	CompactionTokenDelta int                        `json:"compaction_token_delta"`
	CompactionModelCalls int                        `json:"compaction_model_calls"`
	CompactionPrompt     int                        `json:"compaction_prompt_tokens"`
	CompactionCompletion int                        `json:"compaction_completion_tokens"`
	Activity             Activity                   `json:"activity"`
	Timeline             []events.Event             `json:"timeline"`
	Chat                 []ChatEntry                `json:"chat"`
	PendingApproval      *ChatEntry                 `json:"pending_approval,omitempty"`
	ModelUnreachable     *ModelAvailability         `json:"model_unreachable,omitempty"`
	ModelBusy            *ModelAvailability         `json:"model_busy,omitempty"`
	PendingBind          *WorkspaceBindOffer        `json:"pending_bind,omitempty"`
	RunAsYou             bool                       `json:"run_as_you,omitempty"`
	Closed               bool                       `json:"closed"`
	NamePinned           bool                       `json:"name_pinned,omitempty"`
	Stale                bool                       `json:"projection_stale,omitempty"`
	StaleReason          string                     `json:"projection_stale_reason,omitempty"`
}

type ModelAvailability struct {
	Host   string `json:"host"`
	Detail string `json:"detail,omitempty"`
}
type WorkspaceBindOffer struct {
	Dir string `json:"dir"`
}

type Operation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value,omitempty"`
}

type Patch struct {
	SchemaVersion  int         `json:"schema_version"`
	SessionID      string      `json:"session_id"`
	PreviousCursor Cursor      `json:"previous_cursor"`
	Cursor         Cursor      `json:"cursor"`
	Operations     []Operation `json:"operations"`
}

func Empty(sessionID string) Snapshot {
	return Snapshot{
		SchemaVersion: SchemaVersion,
		ID:            sessionID,
		Label:         sessionID,
		Run:           Run{Status: "idle"},
		Messages:      []events.Message{},
		Tools:         []Tool{},
		Activity:      Activity{CompletedStages: []string{}},
		Timeline:      []events.Event{},
		Chat:          []ChatEntry{},
	}
}

// Next is pure: it does not mutate previous, record, or package state.
func Next(previous Snapshot, record Record) (Snapshot, Patch, error) {
	before := previous
	next := previous
	next.SchemaVersion = SchemaVersion
	next.Cursor = record.Cursor
	if next.ID == "" {
		next.ID = record.Event.SessionID
		next.Label = record.Event.SessionID
	}
	data := eventMap(record.Event.Data)

	switch record.Event.Type {
	case events.SessionCreated:
		var wrapper struct {
			Session seed `json:"session"`
		}
		if err := decode(record.Event.Data, &wrapper); err != nil {
			return previous, Patch{}, err
		}
		next = wrapper.Session.snapshot(record.Cursor)
		if record.Event.SessionID != "" {
			next.ID = record.Event.SessionID
		}
	case events.SessionRenamed:
		next.Label = stringValue(data["label"])
		if stringValue(data["by"]) == "user" {
			next.NamePinned = true
		}
	case events.SessionUpdated:
		next.AgentID = firstString(stringValue(data["agent_id"]), next.AgentID)
		next.ServerID = stringValue(data["server_id"])
		if value := stringValue(data["agent_name"]); value != "" {
			next.AgentName = value
		}
		if value := firstString(stringValue(data["b_profile"]), stringValue(data["main_profile"])); value != "" {
			next.BProfile = value
		}
		next.Runnable = boolValue(data["runnable"])
		next.NotRunnableReason = stringValue(data["not_runnable_reason"])
		next.MemoryPath = stringValue(data["memory_path"])
		next.MemoryContent = stringValue(data["memory_content"])
	case events.ProjectInstructions:
		if boolValue(data["lazy"]) {
			if block := stringValue(data["block"]); block != "" {
				if next.ProjectContent != "" {
					next.ProjectContent += "\n\n"
				}
				next.ProjectContent += block
			}
			next.ProjectFiles = appendUniqueStrings(next.ProjectFiles, stringSlice(data["files"])...)
			next.ProjectNotes = append(next.ProjectNotes, stringSlice(data["notes"])...)
		}
	case events.PolicyApproved:
		var policy workspaceinfo.PolicyState
		if decode(record.Event.Data, &policy) == nil {
			next.RepoPolicy = &policy
			next.PendingRepoPolicy = nil
		}
		if data["tools"] != nil {
			var tools []Tool
			if decode(data["tools"], &tools) == nil {
				next.Tools = tools
			}
		}
	case events.PolicyRevoked:
		next.RepoPolicy = nil
	case events.PolicyDenied:
		next.PendingRepoPolicy = nil
	case events.WorkspaceBindRequired:
		next.PendingBind = &WorkspaceBindOffer{Dir: stringValue(data["dir"])}
	case events.WorkspaceBindDecided:
		next.PendingBind = nil
	case events.WorkspaceBound:
		next.PendingBind = nil
		next.Workspace = stringValue(data["workspace_dir"])
		next.WorkspaceDir = next.Workspace
		next.WorkspaceMissing = boolValue(data["workspace_missing"])
		next.ProjectContent = stringValue(data["project_content"])
		next.ProjectFiles = stringSlice(data["project_files"])
		next.ProjectNotes = stringSlice(data["project_notes"])
		next.MemoryPath = stringValue(data["memory_path"])
		next.MemoryContent = stringValue(data["memory_content"])
		_ = decode(data["pending_repo_policy"], &next.PendingRepoPolicy)
		_ = decode(data["repo_policy"], &next.RepoPolicy)
		if data["tools"] != nil {
			_ = decode(data["tools"], &next.Tools)
		}
	case events.MemoryCleared:
		if strings.EqualFold(filepathClean(stringValue(data["dir"])), filepathClean(next.WorkspaceDir)) {
			next.MemoryContent = ""
		}
	case events.SessionReset:
		next.Messages = []events.Message{}
		next.Tools = cloneTools(next.Tools)
		for index := range next.Tools {
			next.Tools[index].Calls = 0
		}
		next.QueuedMessages = 0
		next.ModelTurns = 0
		next.CompactionCount = 0
		next.CompactionTokenDelta = 0
		next.CompactionModelCalls = 0
		next.CompactionPrompt = 0
		next.CompactionCompletion = 0
		next.Run = Run{Status: "idle", MaxTurns: next.Run.MaxTurns}
		next.Activity = Activity{CompletedStages: []string{}}
		next.Timeline = []events.Event{}
		next.Chat = []ChatEntry{}
		next.PendingApproval = nil
		if value := stringValue(data["log_path"]); value != "" {
			next.LogPath = value
		}
	case events.SessionClosed:
		next.Closed = true
		next.RunAsYou = false
	case events.RunQueued:
		next.Run.Status = "queued"
		next.Run.RunID = firstString(data["run_id"], record.Event.RunID)
		next.Run.QueuePosition = intValue(data["position"])
	case events.RunStarted:
		next.Run.Status = "running"
		next.Run.RunID = firstString(data["run_id"], record.Event.RunID)
		next.Run.Turn = 0
		next.Run.QueuePosition = 0
		next.Run.Partial = ""
		next.Run.LastStopReason = ""
		next.QueuedMessages = max(0, next.QueuedMessages-1)
		next.Activity.DispatchAlarm = false
	case events.RunStopping:
		next.Run.Status = "stopping"
	case events.RunStopped:
		status := "idle"
		if boolValue(data["queue_held"]) {
			status = "held"
		}
		next.Run = Run{Status: status, MaxTurns: next.Run.MaxTurns, QueuePosition: next.QueuedMessages, LastStopReason: stringValue(data["reason"])}
		next.Activity.Stage = "wait_user"
		next.Activity.StageState = "enter"
		next.Activity.ActiveTool = ""
		if stringValue(data["reason"]) == "tool_errors" {
			next.Activity.DispatchAlarm = true
		}
		if next.PendingApproval != nil && next.PendingApproval.RunID == record.Event.RunID {
			next.PendingApproval = nil
		}
	case events.ModelUnreachable:
		next.ModelUnreachable = &ModelAvailability{Host: stringValue(data["host"]), Detail: stringValue(data["detail"])}
		next.ModelBusy = nil
	case events.ModelBusy:
		next.ModelBusy = &ModelAvailability{Host: stringValue(data["host"]), Detail: stringValue(data["detail"])}
	case events.ModelReachable:
		if next.ModelUnreachable != nil {
			next.Chat = resolveModelUnreachableNotice(next.Chat, next.ModelUnreachable.Host)
		}
		next.ModelUnreachable = nil
		next.ModelBusy = nil
	case events.ShellGrant, events.FileGrant:
		if stringValue(data["scope"]) == "session" && stringValue(data["identity"]) == "operator" {
			next.RunAsYou = true
		}
	case events.ShellGrantLapsed, events.FileGrantLapsed:
		if stringValue(data["scope"]) == "session" && stringValue(data["identity"]) == "operator" {
			next.RunAsYou = false
		}
	case events.Stage:
		stage, state := stringValue(data["stage"]), stringValue(data["state"])
		next.Run.Turn = intValue(data["turn"])
		next.Activity.StageState = state
		if state == "enter" {
			next.Activity.Stage = stage
			if stage == "assemble" {
				next.Activity.CompletedStages = []string{}
			}
		} else if !contains(next.Activity.CompletedStages, stage) {
			next.Activity.CompletedStages = appendCopy(next.Activity.CompletedStages, stage)
		}
	case events.ModelRequest:
		at := eventMillis(record.Event)
		next.Activity.Stream = &StreamTelemetry{Key: turnKey(record.Event, data), StartedAt: at, LastChunkAt: at, RateStartedAt: at}
		next.Chat = appendChat(next.Chat, ChatEntry{Type: "agent", Key: "turn:" + turnKey(record.Event, data), RunID: record.Event.RunID, Turn: intValue(data["turn"]), AgentRole: "b"})
	case events.ModelProgress:
		next.Activity.Progress = cloneMap(data)
		next.Activity.Stream = touchStream(next.Activity.Stream, record.Event, data, false)
	case events.ModelDelta:
		next.Activity.Stream = touchStream(next.Activity.Stream, record.Event, data, true)
		next.Chat = cloneChat(next.Chat)
		if entry := chatTurn(next.Chat, record.Event.RunID, intValue(data["turn"])); entry != nil {
			if stringValue(data["kind"]) == "reasoning" {
				if entry.ThinkingStartedMS == 0 {
					entry.ThinkingStartedMS = eventMillis(record.Event)
				}
				entry.Reasoning += stringValue(data["text"])
			}
			if stringValue(data["kind"]) == "content" {
				if entry.ThinkingStartedMS > 0 && entry.ThinkingEndedMS == 0 {
					entry.ThinkingEndedMS = eventMillis(record.Event)
				}
				entry.Text += stringValue(data["text"])
			}
		}
		if stringValue(data["kind"]) == "content" {
			next.Run.Partial += stringValue(data["text"])
		}
	case events.ModelResponse:
		next.Run.Partial = ""
		next.ModelTurns++
		next.Activity.LastTimings = mapValue(data["timings"])
		next.Activity.Stream = touchStream(next.Activity.Stream, record.Event, data, false)
		next.Activity.Stream.Done = true
		next.Activity.Stream.ReasoningTokens = intValue(data["reasoning_tokens"])
		next.Activity.Stream.Timings = mapValue(data["timings"])
		next.Chat = cloneChat(next.Chat)
		entry := chatTurn(next.Chat, record.Event.RunID, intValue(data["turn"]))
		if entry == nil {
			next.Chat = append(next.Chat, ChatEntry{Type: "agent", Key: "turn:" + turnKey(record.Event, data), RunID: record.Event.RunID, Turn: intValue(data["turn"]), AgentRole: "b"})
			entry = &next.Chat[len(next.Chat)-1]
		}
		if content := stringValue(data["content"]); content != "" {
			entry.Text = content
		}
		entry.ReasoningTokens = intValue(data["reasoning_tokens"])
		entry.ReasoningTokensEstimated = boolValue(data["reasoning_tokens_estimated"])
		entry.Done = true
		if entry.ThinkingStartedMS > 0 {
			if entry.ThinkingEndedMS == 0 {
				entry.ThinkingEndedMS = eventMillis(record.Event)
			}
			duration := entry.ThinkingEndedMS - entry.ThinkingStartedMS
			entry.ThinkingMS = &duration
		}
		for _, call := range toolCalls(data["tool_calls"]) {
			entry.ToolCallIDs = append(entry.ToolCallIDs, call.ID)
		}
	case events.ToolCallEvent:
		next.Activity.ActiveTool = stringValue(data["name"])
		args := mapValue(data["args"])
		next.Chat = appendChat(next.Chat, ChatEntry{Type: "tool", Key: "tool:" + stringValue(data["call_id"]), CallID: stringValue(data["call_id"]), Name: stringValue(data["name"]), Args: &args})
	case events.ToolResult:
		next.Activity.ActiveTool = ""
		next.Tools = cloneTools(next.Tools)
		for index := range next.Tools {
			if next.Tools[index].Name == stringValue(data["name"]) {
				next.Tools[index].Calls++
				break
			}
		}
		next.Chat = cloneChat(next.Chat)
		if entry := chatCall(next.Chat, stringValue(data["call_id"])); entry != nil {
			entry.Result = cloneMap(data)
			entry.Content = stringValue(data["preview"])
		}
	case events.ToolToggled:
		next.Tools = cloneTools(next.Tools)
		for index := range next.Tools {
			if next.Tools[index].Name == stringValue(data["name"]) {
				next.Tools[index].Enabled = boolValue(data["enabled"])
				break
			}
		}
	case events.MessageAppended:
		var wrapper struct {
			Message events.Message `json:"message"`
		}
		if err := decode(record.Event.Data, &wrapper); err != nil {
			return previous, Patch{}, err
		}
		next.Messages = cloneMessages(next.Messages)
		if wrapper.Message.Category == "summary" {
			at := min(1, len(next.Messages))
			next.Messages = append(next.Messages[:at], append([]events.Message{wrapper.Message}, next.Messages[at:]...)...)
		} else {
			next.Messages = append(next.Messages, wrapper.Message)
		}
		next.Chat = cloneChat(next.Chat)
		if wrapper.Message.Role == "user" {
			next.Chat = append(next.Chat, ChatEntry{Type: "user", Key: "message:" + wrapper.Message.ID, Text: wrapper.Message.Content, Attachments: append([]events.Attachment(nil), wrapper.Message.Attachments...)})
		}
		if wrapper.Message.Role == "assistant" {
			if entry := chatTurnAny(next.Chat, wrapper.Message.Turn); entry != nil {
				if wrapper.Message.Content != "" {
					entry.Text = wrapper.Message.Content
				}
				entry.Reasoning = wrapper.Message.Reasoning
				entry.ToolCallIDs = entry.ToolCallIDs[:0]
				for _, call := range wrapper.Message.ToolCalls {
					entry.ToolCallIDs = append(entry.ToolCallIDs, call.ID)
				}
				entry.Done = true
			}
		}
		if wrapper.Message.Role == "tool" {
			if entry := chatCall(next.Chat, wrapper.Message.ToolCallID); entry != nil {
				entry.Content = wrapper.Message.Content
			}
		}
	case events.MessageUpdated:
		next.Messages = cloneMessages(next.Messages)
		patch := eventMap(data["patch"])
		for index := range next.Messages {
			if next.Messages[index].ID != stringValue(data["id"]) {
				continue
			}
			if value, ok := patch["content"]; ok {
				next.Messages[index].Content = stringValue(value)
			}
			if value, ok := patch["tokens"]; ok {
				next.Messages[index].Tokens = intValue(value)
			}
			if value, ok := patch["elided"]; ok {
				next.Messages[index].Elided = boolValue(value)
			}
			if value, ok := patch["tool_calls"]; ok {
				next.Messages[index].ToolCalls = toolCalls(value)
				next.Chat = cloneChat(next.Chat)
				if entry := chatTurnAny(next.Chat, next.Messages[index].Turn); entry != nil {
					entry.ToolCallIDs = nil
				}
			}
		}
	case events.MessageRemoved:
		id := stringValue(data["id"])
		next.Messages = cloneMessages(next.Messages)
		removed := events.Message{}
		kept := next.Messages[:0]
		for _, message := range next.Messages {
			if message.ID == id {
				removed = message
				continue
			}
			kept = append(kept, message)
		}
		next.Messages = kept
		next.Chat = cloneChat(next.Chat)
		if removed.Role == "user" {
			next.Chat = removeChatKey(next.Chat, "message:"+removed.ID)
		} else if removed.Role == "tool" {
			next.Chat = removeChatKey(next.Chat, "tool:"+removed.ToolCallID)
		} else if removed.Role == "assistant" {
			next.Chat = removeAssistantTurn(next.Chat, removed.Turn)
		}
	case events.MessageQueued:
		next.QueuedMessages++
	case events.BudgetEvent:
		var budget events.Budget
		if err := decode(record.Event.Data, &budget); err != nil {
			return previous, Patch{}, err
		}
		next.Budget = budget
		next.Tools = cloneTools(next.Tools)
		for index := range next.Tools {
			if value, ok := next.Budget.ToolSchemaTokens[next.Tools[index].Name]; ok {
				next.Tools[index].SchemaTokens = value
			}
			if value, ok := next.Budget.ToolMarginalTokens[next.Tools[index].Name]; ok {
				next.Tools[index].MarginalTokens = value
			}
		}
	case events.ApprovalRequired:
		next.Run.Status = "paused"
		event := stripDiagnostic(record.Event)
		next.PendingApproval = &ChatEntry{Type: "notice", Key: "event:" + strconv.FormatInt(record.Event.Seq, 10), RunID: record.Event.RunID, Event: &event}
	case events.ApprovalDecided:
		decision := stringValue(data["decision"])
		if decision != "superseded" && decision != "dismissed" {
			next.Run.Status = "running"
		}
		next.Chat = cloneChat(next.Chat)
		for index := len(next.Chat) - 1; index >= 0; index-- {
			if next.Chat[index].Type == "notice" && next.Chat[index].Event != nil && stringValue(eventMap(next.Chat[index].Event.Data)["call_id"]) == stringValue(data["call_id"]) {
				next.Chat[index].Decision = stringValue(data["decision"])
				break
			}
		}
		if next.PendingApproval != nil && next.PendingApproval.Event != nil && stringValue(eventMap(next.PendingApproval.Event.Data)["call_id"]) == stringValue(data["call_id"]) {
			next.PendingApproval = nil
		}
	case events.CycleDetected:
		next.Activity.DispatchAlarm = true
	case events.WorkspaceConflict:
		next.Activity.AlarmTool = next.Activity.ActiveTool
	case events.Compaction:
		next.Activity.CompactionSerial++
		next.CompactionCount++
		next.CompactionTokenDelta += intValue(data["after"]) - intValue(data["before"])
		if stringValue(data["kind"]) == "summarize" {
			removed := map[string]bool{}
			for _, id := range stringValues(data["affected_ids"]) {
				removed[id] = true
			}
			kept := make([]events.Message, 0, len(next.Messages))
			for _, message := range next.Messages {
				if !removed[message.ID] {
					kept = append(kept, message)
				}
			}
			next.Messages = kept
		}
	case events.CompactionSummary:
		var attempt events.CompactionSummaryData
		if err := decode(record.Event.Data, &attempt); err != nil {
			return previous, Patch{}, err
		}
		if attempt.Dispatched {
			next.CompactionModelCalls++
			next.CompactionPrompt += attempt.Usage.PromptTokens
			next.CompactionCompletion += attempt.Usage.CompletionTokens
		}
	case events.MemoryNoted:
		if stringValue(data["target"]) == "agent" {
			next.AgentMemoryPath = firstString(data["path"], next.AgentMemoryPath)
			next.AgentMemoryContent = strings.TrimRight(next.AgentMemoryContent, "\r\n") + "\n- " + stringValue(data["note"]) + "\n"
		} else {
			next.MemoryPath = firstString(data["path"], next.MemoryPath)
			next.MemoryContent = strings.TrimRight(next.MemoryContent, "\r\n") + "\n- " + stringValue(data["note"]) + "\n"
		}
	}
	if chatNotice(record.Event.Type) {
		event := stripDiagnostic(record.Event)
		role := ""
		if value := stringValue(data["role"]); value == "c" || value == "aux" {
			role = "c"
		}
		entry := ChatEntry{Type: "notice", Key: "event:" + strconv.FormatInt(record.Event.Seq, 10), RunID: record.Event.RunID, Event: &event, AgentRole: role}
		if record.Event.Type == events.RunStopped && stringValue(data["reason"]) == "model_unreachable" && next.ModelUnreachable != nil {
			entry.Text = next.ModelUnreachable.Host
			next.Chat = upsertModelUnreachableNotice(next.Chat, entry)
		} else {
			next.Chat = appendChat(next.Chat, entry)
		}
	}
	if record.Event.Type == events.RunStopped {
		next.Chat = cloneChat(next.Chat)
		for index := range next.Chat {
			if next.Chat[index].Type == "agent" && next.Chat[index].RunID == record.Event.RunID {
				next.Chat[index].Done = true
			}
		}
	}

	// Timeline is an unbounded durable operational view. Streaming fragments are
	// retained only while their turn is live; completed response content lives in
	// messages/model.response and cannot evict operational history.
	next.Timeline = append(append([]events.Event(nil), next.Timeline...), stripDiagnostic(record.Event))
	if record.Event.Type == events.ModelResponse {
		next.Timeline = discardStream(next.Timeline, record.Event.RunID, intValue(data["turn"]))
	} else if record.Event.Type == events.RunStopped {
		next.Timeline = discardRunStream(next.Timeline, record.Event.RunID)
	}

	next.Cursor = record.Cursor
	return next, diff(before, next), nil
}

func touchStream(current *StreamTelemetry, event events.Event, data map[string]any, delta bool) *StreamTelemetry {
	value := &StreamTelemetry{}
	if current != nil {
		encoded, _ := json.Marshal(current)
		_ = json.Unmarshal(encoded, value)
	}
	at, key := eventMillis(event), turnKey(event, data)
	if value.Key != key {
		value = &StreamTelemetry{Key: key, StartedAt: at, LastChunkAt: at, RateStartedAt: at}
	}
	value.LastChunkAt, value.HasChunk = at, true
	if delta {
		chars := len([]rune(stringValue(data["text"])))
		if stringValue(data["kind"]) == "reasoning" {
			value.ReasoningChars += chars
		}
		value.TotalChars += chars
		if at-value.RateStartedAt > 5000 {
			value.RateStartedAt, value.RateChars = at, 0
		}
		value.RateChars += chars
		seconds := math.Max(.25, float64(at-value.RateStartedAt)/1000)
		value.Rate = int(math.Round(float64(value.RateChars) / 3.6 / seconds))
	}
	return value
}

func eventMillis(event events.Event) int64 {
	value, err := time.Parse(time.RFC3339Nano, event.TS)
	if err != nil {
		return 0
	}
	return value.UnixMilli()
}
func turnKey(event events.Event, data map[string]any) string {
	return event.RunID + ":" + strconv.Itoa(intValue(data["turn"]))
}

type seed struct {
	ID                   string                     `json:"id"`
	Label                string                     `json:"label"`
	AgentID              string                     `json:"agent_id"`
	ServerID             string                     `json:"server_id"`
	AgentName            string                     `json:"agent_name"`
	MainProfile          string                     `json:"main_profile"`
	BProfile             string                     `json:"b_profile"`
	CreatedAt            string                     `json:"created_at"`
	Closed               bool                       `json:"closed"`
	NamePinned           bool                       `json:"name_pinned,omitempty"`
	Workspace            string                     `json:"workspace"`
	WorkspaceDir         string                     `json:"workspace_dir"`
	WorkspaceMissing     bool                       `json:"workspace_missing"`
	ProjectContent       string                     `json:"project_content"`
	ProjectFiles         []string                   `json:"project_files"`
	ProjectNotes         []string                   `json:"project_notes"`
	PendingRepoPolicy    *workspaceinfo.PolicyState `json:"pending_repo_policy,omitempty"`
	RepoPolicy           *workspaceinfo.PolicyState `json:"repo_policy,omitempty"`
	Run                  Run                        `json:"run"`
	Tools                []Tool                     `json:"tools"`
	Messages             []events.Message           `json:"messages"`
	Budget               events.Budget              `json:"budget"`
	QueuedMessages       int                        `json:"queued_messages"`
	Runnable             bool                       `json:"runnable"`
	NotRunnableReason    string                     `json:"not_runnable_reason"`
	MemoryPath           string                     `json:"memory_path"`
	MemoryContent        string                     `json:"memory_content"`
	AgentMemoryPath      string                     `json:"agent_memory_path"`
	AgentMemoryContent   string                     `json:"agent_memory_content"`
	LogPath              string                     `json:"log_path"`
	ModelTurns           int                        `json:"model_turns"`
	CompactionCount      int                        `json:"compaction_count"`
	CompactionTokenDelta int                        `json:"compaction_token_delta"`
	CompactionModelCalls int                        `json:"compaction_model_calls"`
	CompactionPrompt     int                        `json:"compaction_prompt_tokens"`
	CompactionCompletion int                        `json:"compaction_completion_tokens"`
}

func (value seed) snapshot(cursor Cursor) Snapshot {
	return Snapshot{
		SchemaVersion: SchemaVersion, Cursor: cursor, Complete: true,
		ID: value.ID, Label: value.Label, AgentID: value.AgentID, ServerID: value.ServerID, AgentName: value.AgentName, BProfile: firstString(value.BProfile, value.MainProfile), CreatedAt: value.CreatedAt, Closed: value.Closed, NamePinned: value.NamePinned, Workspace: value.Workspace, WorkspaceDir: firstString(value.WorkspaceDir, value.Workspace), WorkspaceMissing: value.WorkspaceMissing, ProjectContent: value.ProjectContent, ProjectFiles: append([]string(nil), value.ProjectFiles...), ProjectNotes: append([]string(nil), value.ProjectNotes...), PendingRepoPolicy: value.PendingRepoPolicy, RepoPolicy: value.RepoPolicy,
		Run: value.Run, Tools: cloneTools(value.Tools), Messages: cloneMessages(value.Messages), Budget: value.Budget,
		QueuedMessages: value.QueuedMessages, Runnable: value.Runnable, NotRunnableReason: value.NotRunnableReason,
		MemoryPath: value.MemoryPath, MemoryContent: value.MemoryContent, AgentMemoryPath: value.AgentMemoryPath, AgentMemoryContent: value.AgentMemoryContent, LogPath: value.LogPath,
		ModelTurns: value.ModelTurns, CompactionCount: value.CompactionCount, CompactionTokenDelta: value.CompactionTokenDelta,
		CompactionModelCalls: value.CompactionModelCalls, CompactionPrompt: value.CompactionPrompt,
		CompactionCompletion: value.CompactionCompletion, Activity: Activity{CompletedStages: []string{}},
		Timeline: []events.Event{},
		Chat:     []ChatEntry{},
	}
}

func diff(before, after Snapshot) Patch {
	patch := Patch{SchemaVersion: SchemaVersion, SessionID: after.ID, PreviousCursor: before.Cursor, Cursor: after.Cursor, Operations: []Operation{}}
	fields := []struct {
		path        string
		before, now any
	}{
		{"complete", before.Complete, after.Complete}, {"id", before.ID, after.ID}, {"label", before.Label, after.Label},
		{"agent_id", before.AgentID, after.AgentID}, {"server_id", before.ServerID, after.ServerID}, {"agent_name", before.AgentName, after.AgentName},
		{"b_profile", before.BProfile, after.BProfile}, {"created_at", before.CreatedAt, after.CreatedAt}, {"workspace", before.Workspace, after.Workspace}, {"workspace_dir", before.WorkspaceDir, after.WorkspaceDir}, {"workspace_missing", before.WorkspaceMissing, after.WorkspaceMissing}, {"project_content", before.ProjectContent, after.ProjectContent}, {"project_files", before.ProjectFiles, after.ProjectFiles}, {"project_notes", before.ProjectNotes, after.ProjectNotes}, {"pending_repo_policy", before.PendingRepoPolicy, after.PendingRepoPolicy}, {"repo_policy", before.RepoPolicy, after.RepoPolicy},
		{"tools", before.Tools, after.Tools}, {"messages", before.Messages, after.Messages}, {"budget", before.Budget, after.Budget},
		{"queued_messages", before.QueuedMessages, after.QueuedMessages}, {"runnable", before.Runnable, after.Runnable},
		{"not_runnable_reason", before.NotRunnableReason, after.NotRunnableReason}, {"memory_path", before.MemoryPath, after.MemoryPath},
		{"memory_content", before.MemoryContent, after.MemoryContent}, {"agent_memory_path", before.AgentMemoryPath, after.AgentMemoryPath}, {"agent_memory_content", before.AgentMemoryContent, after.AgentMemoryContent}, {"log_path", before.LogPath, after.LogPath},
		{"model_turns", before.ModelTurns, after.ModelTurns}, {"compaction_count", before.CompactionCount, after.CompactionCount},
		{"compaction_token_delta", before.CompactionTokenDelta, after.CompactionTokenDelta},
		{"compaction_model_calls", before.CompactionModelCalls, after.CompactionModelCalls},
		{"compaction_prompt_tokens", before.CompactionPrompt, after.CompactionPrompt},
		{"compaction_completion_tokens", before.CompactionCompletion, after.CompactionCompletion},
		{"activity", before.Activity, after.Activity}, {"pending_approval", before.PendingApproval, after.PendingApproval}, {"pending_bind", before.PendingBind, after.PendingBind}, {"model_unreachable", before.ModelUnreachable, after.ModelUnreachable}, {"model_busy", before.ModelBusy, after.ModelBusy}, {"run_as_you", before.RunAsYou, after.RunAsYou}, {"closed", before.Closed, after.Closed}, {"name_pinned", before.NamePinned, after.NamePinned},
		{"projection_stale", before.Stale, after.Stale}, {"projection_stale_reason", before.StaleReason, after.StaleReason},
	}
	patch.Operations = append(patch.Operations, diffRun(before.Run, after.Run)...)
	patch.Operations = append(patch.Operations, diffChat(before.Chat, after.Chat)...)
	for _, field := range fields {
		if reflect.DeepEqual(field.before, field.now) {
			continue
		}
		value, _ := json.Marshal(field.now)
		patch.Operations = append(patch.Operations, Operation{Op: "replace", Path: "/" + field.path, Value: value})
	}
	if !reflect.DeepEqual(before.Timeline, after.Timeline) {
		if len(after.Timeline) == len(before.Timeline)+1 && reflect.DeepEqual(before.Timeline, after.Timeline[:len(before.Timeline)]) {
			value, _ := json.Marshal(after.Timeline[len(after.Timeline)-1])
			patch.Operations = append(patch.Operations, Operation{Op: "append", Path: "/timeline", Value: value})
		} else {
			value, _ := json.Marshal(after.Timeline)
			patch.Operations = append(patch.Operations, Operation{Op: "replace", Path: "/timeline", Value: value})
		}
	}
	return patch
}

func diffRun(before, after Run) []Operation {
	if reflect.DeepEqual(before, after) {
		return nil
	}
	withoutBefore, withoutAfter := before, after
	withoutBefore.Partial, withoutAfter.Partial = "", ""
	if reflect.DeepEqual(withoutBefore, withoutAfter) && strings.HasPrefix(after.Partial, before.Partial) {
		value, _ := json.Marshal(strings.TrimPrefix(after.Partial, before.Partial))
		return []Operation{{Op: "append", Path: "/run/partial", Value: value}}
	}
	value, _ := json.Marshal(after)
	return []Operation{{Op: "replace", Path: "/run", Value: value}}
}

func diffChat(before, after []ChatEntry) []Operation {
	if reflect.DeepEqual(before, after) {
		return nil
	}
	if len(after) == len(before)+1 && reflect.DeepEqual(before, after[:len(before)]) {
		value, _ := json.Marshal(after[len(after)-1])
		return []Operation{{Op: "append", Path: "/chat", Value: value}}
	}
	if len(after) == len(before) && len(after) > 0 && reflect.DeepEqual(before[:len(before)-1], after[:len(after)-1]) && before[len(before)-1].Key == after[len(after)-1].Key {
		left, right := before[len(before)-1], after[len(after)-1]
		leftReason, rightReason := left.Reasoning, right.Reasoning
		left.Reasoning, right.Reasoning = "", ""
		if reflect.DeepEqual(left, right) && strings.HasPrefix(rightReason, leftReason) {
			value, _ := json.Marshal(strings.TrimPrefix(rightReason, leftReason))
			return []Operation{{Op: "append", Path: "/chat/" + pointer(right.Key) + "/reasoning", Value: value}}
		}
		left, right = before[len(before)-1], after[len(after)-1]
		leftText, rightText := left.Text, right.Text
		left.Text, right.Text = "", ""
		if reflect.DeepEqual(left, right) && strings.HasPrefix(rightText, leftText) {
			value, _ := json.Marshal(strings.TrimPrefix(rightText, leftText))
			return []Operation{{Op: "append", Path: "/chat/" + pointer(right.Key) + "/text", Value: value}}
		}
		value, _ := json.Marshal(after[len(after)-1])
		return []Operation{{Op: "upsert", Path: "/chat/" + pointer(after[len(after)-1].Key), Value: value}}
	}
	if len(after) == len(before) {
		operations := make([]Operation, 0, len(after))
		for index := range after {
			if before[index].Key != after[index].Key {
				operations = nil
				break
			}
			if reflect.DeepEqual(before[index], after[index]) {
				continue
			}
			value, _ := json.Marshal(after[index])
			operations = append(operations, Operation{Op: "upsert", Path: "/chat/" + pointer(after[index].Key), Value: value})
		}
		if operations != nil {
			return operations
		}
	}
	value, _ := json.Marshal(after)
	return []Operation{{Op: "replace", Path: "/chat", Value: value}}
}
func pointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func stripDiagnostic(event events.Event) events.Event {
	event.Body = nil
	event.Raw = nil
	return event
}

func discardStream(values []events.Event, runID string, turn int) []events.Event {
	out := make([]events.Event, 0, len(values))
	for _, event := range values {
		if (event.Type == events.ModelDelta || event.Type == events.ModelProgress) && event.RunID == runID && intValue(eventMap(event.Data)["turn"]) == turn {
			continue
		}
		out = append(out, event)
	}
	return out
}

func discardRunStream(values []events.Event, runID string) []events.Event {
	out := make([]events.Event, 0, len(values))
	for _, event := range values {
		if (event.Type == events.ModelDelta || event.Type == events.ModelProgress) && event.RunID == runID {
			continue
		}
		out = append(out, event)
	}
	return out
}

func appendChat(values []ChatEntry, entry ChatEntry) []ChatEntry {
	result := cloneChat(values)
	return append(result, entry)
}
func upsertModelUnreachableNotice(values []ChatEntry, entry ChatEntry) []ChatEntry {
	result := cloneChat(values)
	for index := len(result) - 1; index >= 0; index-- {
		current := result[index]
		if current.Type == "notice" && current.Event != nil && current.Event.Type == events.RunStopped &&
			stringValue(eventMap(current.Event.Data)["reason"]) == "model_unreachable" && current.Text == entry.Text && current.Decision != "resolved" {
			entry.Key = current.Key
			result[index] = entry
			return result
		}
	}
	return append(result, entry)
}
func resolveModelUnreachableNotice(values []ChatEntry, host string) []ChatEntry {
	result := cloneChat(values)
	for index := len(result) - 1; index >= 0; index-- {
		entry := &result[index]
		if entry.Type == "notice" && entry.Event != nil && entry.Event.Type == events.RunStopped &&
			stringValue(eventMap(entry.Event.Data)["reason"]) == "model_unreachable" && entry.Text == host && entry.Decision != "resolved" {
			entry.Decision = "resolved"
			break
		}
	}
	return result
}
func removeChatKey(values []ChatEntry, key string) []ChatEntry {
	result := make([]ChatEntry, 0, len(values))
	for _, entry := range values {
		if entry.Key != key {
			result = append(result, entry)
		}
	}
	return result
}
func removeAssistantTurn(values []ChatEntry, turn int) []ChatEntry {
	for index := len(values) - 1; index >= 0; index-- {
		if values[index].Type == "agent" && values[index].Turn == turn {
			return append(values[:index], values[index+1:]...)
		}
	}
	return values
}
func cloneChat(values []ChatEntry) []ChatEntry {
	encoded, _ := json.Marshal(values)
	var result []ChatEntry
	_ = json.Unmarshal(encoded, &result)
	if result == nil {
		result = []ChatEntry{}
	}
	return result
}
func chatTurn(values []ChatEntry, runID string, turn int) *ChatEntry {
	for index := len(values) - 1; index >= 0; index-- {
		if values[index].Type == "agent" && values[index].RunID == runID && values[index].Turn == turn {
			return &values[index]
		}
	}
	return nil
}
func chatTurnAny(values []ChatEntry, turn int) *ChatEntry {
	for index := len(values) - 1; index >= 0; index-- {
		if values[index].Type == "agent" && values[index].Turn == turn {
			return &values[index]
		}
	}
	return nil
}
func chatCall(values []ChatEntry, callID string) *ChatEntry {
	for index := len(values) - 1; index >= 0; index-- {
		if values[index].Type == "tool" && values[index].CallID == callID {
			return &values[index]
		}
	}
	return nil
}
func toolCalls(value any) []events.ToolCall {
	var result []events.ToolCall
	_ = decode(value, &result)
	return result
}
func chatNotice(value string) bool {
	switch value {
	case events.RunStopping, events.RunStopped, events.RunAborted, events.RunQueued, events.MessageQueued, events.ModelRetry, events.Compaction, events.WorkspaceConflict, events.ApprovalRequired, events.MemoryNoted, events.MemoryCleared, events.ProjectInstructions, events.PolicyApproved, events.PolicyDenied, events.PolicyRevoked, events.OperatorContext, events.SigningApplied, events.FilesDelivered, events.ShellGrant, events.ShellGrantLapsed, events.FileGrant, events.FileGrantLapsed:
		return true
	}
	return false
}

func stringSlice(value any) []string { var out []string; _ = decode(value, &out); return out }
func appendUniqueStrings(values []string, additions ...string) []string {
	for _, addition := range additions {
		found := false
		for _, value := range values {
			if value == addition {
				found = true
				break
			}
		}
		if !found {
			values = append(values, addition)
		}
	}
	return values
}
func filepathClean(value string) string { return strings.ToLower(strings.ReplaceAll(value, "/", "\\")) }

func decode(value, target any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func eventMap(value any) map[string]any {
	if result, ok := value.(map[string]any); ok {
		return result
	}
	data, _ := json.Marshal(value)
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	return result
}

func mapValue(value any) map[string]any { return cloneMap(eventMap(value)) }
func stringValue(value any) string      { result, _ := value.(string); return result }
func boolValue(value any) bool          { result, _ := value.(bool); return result }
func intValue(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case float64:
		return int(number)
	case json.Number:
		result, _ := number.Int64()
		return int(result)
	default:
		return 0
	}
}
func firstString(values ...any) string {
	for _, value := range values {
		if result := stringValue(value); result != "" {
			return result
		}
	}
	return ""
}
func stringValues(value any) []string {
	if values, ok := value.([]string); ok {
		return append([]string(nil), values...)
	}
	raw, _ := value.([]any)
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		result = append(result, stringValue(item))
	}
	return result
}
func cloneMessages(values []events.Message) []events.Message {
	return append([]events.Message(nil), values...)
}
func cloneTools(values []Tool) []Tool { return append([]Tool(nil), values...) }
func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
func appendCopy(values []string, value string) []string {
	return append(append([]string(nil), values...), value)
}
func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
