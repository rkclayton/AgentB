package events

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ReplayTool struct {
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	Calls        int    `json:"calls"`
	SchemaTokens int    `json:"schema_tokens"`
}

type ReplayRun struct {
	Status        string `json:"status"`
	RunID         string `json:"run_id"`
	Turn          int    `json:"turn"`
	MaxTurns      int    `json:"max_turns"`
	QueuePosition int    `json:"queue_position"`
	Partial       string `json:"partial"`
}

type ReplaySession struct {
	ID                string       `json:"id"`
	Label             string       `json:"label"`
	ServerID          string       `json:"server_id"`
	Workspace         string       `json:"workspace"`
	Run               ReplayRun    `json:"run"`
	Tools             []ReplayTool `json:"tools"`
	Messages          []Message    `json:"messages"`
	Budget            Budget       `json:"budget"`
	Timeline          []Event      `json:"timeline"`
	QueuedMessages    int          `json:"queued_messages"`
	Runnable          bool         `json:"runnable"`
	NotRunnableReason string       `json:"not_runnable_reason"`
	MemoryPath        string       `json:"memory_path"`
	MemoryContent     string       `json:"memory_content"`
	LogPath           string       `json:"log_path"`
}

type Replay struct {
	Events   []Event
	Initial  map[string]ReplaySession
	Sessions map[string]ReplaySession
}

// ReduceReplay mirrors web/js/bus.js. Event-state changes belong in both reducers.
func LoadReplay(paths []string) (*Replay, error) {
	result := &Replay{Initial: map[string]ReplaySession{}, Sessions: map[string]ReplaySession{}}
	used := map[string]bool{}
	for _, rawPath := range paths {
		path := strings.TrimSpace(rawPath)
		if path == "" {
			continue
		}
		events, err := readEvents(path)
		if err != nil {
			return nil, err
		}
		if len(events) == 0 {
			return nil, fmt.Errorf("replay %s: no events", path)
		}
		oldID := ""
		for _, event := range events {
			if event.SessionID != "" {
				oldID = event.SessionID
				break
			}
		}
		id := oldID
		if id == "" {
			id = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		base := id
		for suffix := 2; used[id]; suffix++ {
			id = fmt.Sprintf("%s-%d", base, suffix)
		}
		used[id] = true
		initial := ReplaySession{ID: id, Label: id, Run: ReplayRun{Status: "replay"}, Runnable: true, LogPath: path, Messages: []Message{}, Tools: []ReplayTool{}, Timeline: []Event{}}
		for index := range events {
			if events[index].SessionID == oldID || events[index].SessionID == "" {
				events[index].SessionID = id
			}
			if events[index].Type == SessionCreated {
				var data struct {
					Session ReplaySession `json:"session"`
				}
				if decodeReplay(events[index].Data, &data) == nil {
					initial = data.Session
					initial.ID = id
					initial.Run.Status = "replay"
					initial.Messages = []Message{}
					initial.Timeline = []Event{}
					initial.LogPath = path
				}
			}
		}
		result.Initial[id] = initial
		result.Sessions[id] = cloneReplaySession(initial)
		result.Events = append(result.Events, events...)
	}
	if len(result.Events) == 0 {
		return nil, fmt.Errorf("replay: provide at least one JSONL path")
	}
	sort.SliceStable(result.Events, func(i, j int) bool {
		left, _ := time.Parse(time.RFC3339Nano, result.Events[i].TS)
		right, _ := time.Parse(time.RFC3339Nano, result.Events[j].TS)
		return left.Before(right)
	})
	for index := range result.Events {
		result.Events[index].Seq = int64(index + 1)
		ReduceReplay(result.Sessions, result.Events[index])
	}
	for id, item := range result.Sessions {
		item.Run.Status = "replay"
		item.Run.Partial = ""
		result.Sessions[id] = item
	}
	return result, nil
}

func readEvents(path string) ([]Event, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("replay %s: %w", path, err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	values := []Event{}
	line := 0
	for scanner.Scan() {
		line++
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("replay %s line %d: %w", path, line, err)
		}
		if event.Type == "" || event.TS == "" {
			return nil, fmt.Errorf("replay %s line %d: event type and ts are required", path, line)
		}
		if _, err := time.Parse(time.RFC3339Nano, event.TS); err != nil {
			return nil, fmt.Errorf("replay %s line %d: invalid ts: %w", path, line, err)
		}
		values = append(values, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("replay %s: %w", path, err)
	}
	return values, nil
}

func ReduceReplay(sessions map[string]ReplaySession, event Event) {
	item, ok := sessions[event.SessionID]
	if !ok {
		return
	}
	data := replayMap(event.Data)
	switch event.Type {
	case SessionCreated:
		var wrapper struct {
			Session ReplaySession `json:"session"`
		}
		if decodeReplay(event.Data, &wrapper) == nil {
			messages, timeline, logPath := item.Messages, item.Timeline, item.LogPath
			item = wrapper.Session
			item.ID = event.SessionID
			item.Messages, item.Timeline, item.LogPath = messages, timeline, logPath
		}
	case SessionRenamed:
		item.Label = replayString(data["label"])
	case SessionUpdated:
		item.ServerID = replayString(data["server_id"])
		item.Runnable = replayBool(data["runnable"])
		item.NotRunnableReason = replayString(data["not_runnable_reason"])
		item.MemoryPath = replayString(data["memory_path"])
		item.MemoryContent = replayString(data["memory_content"])
	case SessionReset:
		item.Messages = []Message{}
		item.Timeline = []Event{}
		item.QueuedMessages = 0
		item.Run = ReplayRun{Status: "replay", MaxTurns: item.Run.MaxTurns}
	case RunQueued:
		item.Run.RunID = event.RunID
		item.Run.QueuePosition = replayInt(data["position"])
	case RunStarted:
		item.Run.RunID = event.RunID
		item.Run.Turn = 0
		item.Run.QueuePosition = 0
		item.QueuedMessages = max(0, item.QueuedMessages-1)
	case RunStopped:
		item.Run.RunID = event.RunID
		item.Run.Partial = ""
	case Stage:
		item.Run.Turn = replayInt(data["turn"])
	case ModelDelta:
		if replayString(data["kind"]) == "content" {
			item.Run.Partial += replayString(data["text"])
		}
	case ModelResponse:
		item.Run.Partial = ""
	case ToolResult:
		name := replayString(data["name"])
		for index := range item.Tools {
			if item.Tools[index].Name == name {
				item.Tools[index].Calls++
			}
		}
	case ToolToggled:
		name, enabled := replayString(data["name"]), replayBool(data["enabled"])
		for index := range item.Tools {
			if item.Tools[index].Name == name {
				item.Tools[index].Enabled = enabled
			}
		}
	case MessageAppended:
		var wrapper struct {
			Message Message `json:"message"`
		}
		if decodeReplay(event.Data, &wrapper) == nil {
			if wrapper.Message.Category == "summary" {
				at := min(1, len(item.Messages))
				item.Messages = append(item.Messages[:at], append([]Message{wrapper.Message}, item.Messages[at:]...)...)
			} else {
				item.Messages = append(item.Messages, wrapper.Message)
			}
		}
	case MessageUpdated:
		id := replayString(data["id"])
		patch := replayMap(data["patch"])
		for index := range item.Messages {
			if item.Messages[index].ID != id {
				continue
			}
			if value, found := patch["content"]; found {
				item.Messages[index].Content = replayString(value)
			}
			if value, found := patch["tokens"]; found {
				item.Messages[index].Tokens = replayInt(value)
			}
			if value, found := patch["elided"]; found {
				item.Messages[index].Elided = replayBool(value)
			}
		}
	case MessageQueued:
		item.QueuedMessages++
	case BudgetEvent:
		_ = decodeReplay(event.Data, &item.Budget)
	case Compaction:
		if replayString(data["kind"]) == "summarize" {
			removed := map[string]bool{}
			for _, id := range replayStrings(data["affected_ids"]) {
				removed[id] = true
			}
			kept := item.Messages[:0]
			for _, message := range item.Messages {
				if !removed[message.ID] {
					kept = append(kept, message)
				}
			}
			item.Messages = kept
		}
	case MemoryNoted:
		item.MemoryPath = replayString(data["path"])
		item.MemoryContent = strings.TrimRight(item.MemoryContent, "\r\n") + "\n- " + replayString(data["note"]) + "\n"
	}
	item.Run.Status = "replay"
	item.Timeline = append(item.Timeline, event)
	sessions[event.SessionID] = item
}

func cloneReplaySession(value ReplaySession) ReplaySession {
	data, _ := json.Marshal(value)
	var out ReplaySession
	_ = json.Unmarshal(data, &out)
	return out
}
func decodeReplay(value, target any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
func replayMap(value any) map[string]any { result, _ := value.(map[string]any); return result }
func replayString(value any) string      { result, _ := value.(string); return result }
func replayBool(value any) bool          { result, _ := value.(bool); return result }
func replayInt(value any) int {
	if n, ok := value.(float64); ok {
		return int(n)
	}
	if n, ok := value.(int); ok {
		return n
	}
	return 0
}
func replayStrings(value any) []string {
	raw, _ := value.([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		out = append(out, replayString(item))
	}
	return out
}
