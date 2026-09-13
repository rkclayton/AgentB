package agent

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"harness/internal/events"
	"harness/internal/llm"
	"harness/internal/session"
)

const harnessAbortRecordPrefix = "[HARNESS ABORT RECORD — verify every possibly-written path before trusting it]"

const cancellationBound = 2 * time.Second

type flightState struct {
	RunID                string
	Turn                 int
	Stage                string
	ToolName             string
	ToolArguments        string
	PartialReasoning     string
	PartialContent       string
	PartialToolArguments map[int]string
	PartialToolNames     map[int]string
	PartialToolOutput    string
	PossiblePaths        []string
	PathsComplete        bool
}

type flightBook struct {
	mu       sync.Mutex
	active   map[string]flightState
	recorded map[string]bool
}

func newFlightBook() *flightBook {
	return &flightBook{active: map[string]flightState{}, recorded: map[string]bool{}}
}

func flightKey(sessionID, runID string) string { return sessionID + "\x00" + runID }

func (r *Runner) beginFlight(sessionID, runID string) {
	r.flights.mu.Lock()
	r.flights.active[sessionID] = flightState{RunID: runID, Stage: "starting", PathsComplete: true, PartialToolArguments: map[int]string{}, PartialToolNames: map[int]string{}}
	delete(r.flights.recorded, flightKey(sessionID, runID))
	r.flights.mu.Unlock()
}

func (r *Runner) endFlight(sessionID, runID string) {
	r.flights.mu.Lock()
	if state, ok := r.flights.active[sessionID]; ok && state.RunID == runID {
		delete(r.flights.active, sessionID)
	}
	delete(r.flights.recorded, flightKey(sessionID, runID))
	r.flights.mu.Unlock()
}

func (r *Runner) setFlightStage(sessionID, runID string, turn int, stage string) {
	r.flights.mu.Lock()
	state, ok := r.flights.active[sessionID]
	if ok && state.RunID == runID {
		state.Turn, state.Stage = turn, stage
		if stage == "assemble" {
			state.PartialReasoning, state.PartialContent = "", ""
			state.PartialToolArguments, state.PartialToolNames = map[int]string{}, map[int]string{}
		}
		if stage != "execute" {
			state.ToolName, state.ToolArguments, state.PartialToolOutput = "", "", ""
			state.PossiblePaths, state.PathsComplete = nil, true
		}
		r.flights.active[sessionID] = state
	}
	r.flights.mu.Unlock()
}

func (r *Runner) addFlightDelta(sessionID, runID string, delta llm.Delta) {
	r.flights.mu.Lock()
	state, ok := r.flights.active[sessionID]
	if ok && state.RunID == runID {
		switch delta.Kind {
		case "reasoning":
			state.PartialReasoning += delta.Text
		case "content":
			state.PartialContent += delta.Text
		case "tool_call":
			if delta.Name != "" {
				state.PartialToolNames[delta.Index] = delta.Name
			}
			if state.PartialToolNames[delta.Index] == "call_service" {
				state.PartialToolArguments[delta.Index] = "[redacted: call_service arguments are not durable until complete]"
			} else {
				state.PartialToolArguments[delta.Index] += delta.Text
			}
		}
		r.flights.active[sessionID] = state
	}
	r.flights.mu.Unlock()
}

func (r *Runner) setFlightTool(sessionID, runID string, turn int, call events.ToolCall, args map[string]any) {
	r.flights.mu.Lock()
	state, ok := r.flights.active[sessionID]
	if ok && state.RunID == runID {
		state.Turn, state.Stage = turn, "execute"
		state.ToolName, state.ToolArguments, state.PartialToolOutput = call.Name, call.Arguments, ""
		state.PossiblePaths, state.PathsComplete = possibleWrittenPaths(call.Name, args)
		r.flights.active[sessionID] = state
	}
	r.flights.mu.Unlock()
}

func (r *Runner) setFlightToolOutput(sessionID, runID, output string) {
	r.flights.mu.Lock()
	state, ok := r.flights.active[sessionID]
	if ok && state.RunID == runID {
		state.PartialToolOutput = output
		r.flights.active[sessionID] = state
	}
	r.flights.mu.Unlock()
}

func possibleWrittenPaths(name string, args map[string]any) ([]string, bool) {
	switch name {
	case "write_file", "edit_file":
		if path, _ := args["path"].(string); path != "" {
			return []string{path}, true
		}
		return nil, false
	case "shell", "run_script":
		return []string{"unknown: command or script may write arbitrary paths"}, false
	default:
		return nil, true
	}
}

var partialPathPattern = regexp.MustCompile(`"path"\s*:\s*("(?:\\.|[^"\\])*")`)

func possiblePartialPaths(calls []map[string]any) ([]string, bool) {
	paths, complete := []string{}, true
	for _, call := range calls {
		name, _ := call["name"].(string)
		arguments, _ := call["arguments"].(string)
		switch name {
		case "write_file", "edit_file":
			match := partialPathPattern.FindStringSubmatch(arguments)
			if len(match) != 2 {
				complete = false
				continue
			}
			path, err := strconv.Unquote(match[1])
			if err != nil {
				complete = false
				continue
			}
			paths = append(paths, path)
		case "shell", "run_script":
			paths, complete = append(paths, "unknown: command or script may write arbitrary paths"), false
		}
	}
	return paths, complete
}

func (r *Runner) abortReason(sessionID, runID string) string {
	r.flights.mu.Lock()
	defer r.flights.mu.Unlock()
	state, ok := r.flights.active[sessionID]
	if !ok || state.RunID != runID {
		return "aborted_mid_run"
	}
	if state.Stage == "execute" && state.ToolName != "" {
		return "aborted_mid_tool"
	}
	if state.Stage == "call_model" {
		return "aborted_mid_model"
	}
	return "aborted_mid_run"
}

func (r *Runner) recordAbort(s *session.Session, runID, reason, detail string) string {
	key := flightKey(s.ID, runID)
	r.flights.mu.Lock()
	if r.flights.recorded[key] {
		r.flights.mu.Unlock()
		return detail
	}
	state := r.flights.active[s.ID]
	r.flights.recorded[key] = true
	r.flights.mu.Unlock()

	partialCalls := make([]map[string]any, 0, len(state.PartialToolArguments))
	indexes := make([]int, 0, len(state.PartialToolArguments))
	for index := range state.PartialToolArguments {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		partialCalls = append(partialCalls, map[string]any{"index": index, "name": state.PartialToolNames[index], "arguments": state.PartialToolArguments[index]})
	}
	if len(state.PossiblePaths) == 0 && len(partialCalls) > 0 {
		state.PossiblePaths, state.PathsComplete = possiblePartialPaths(partialCalls)
	}
	data := map[string]any{
		"reason": reason, "detail": detail, "turn": state.Turn, "stage": state.Stage,
		"tool_in_flight":         map[string]any{"name": state.ToolName, "arguments": state.ToolArguments},
		"partial_output":         map[string]any{"reasoning": state.PartialReasoning, "content": state.PartialContent, "tool_calls": partialCalls, "tool_result": state.PartialToolOutput},
		"possibly_written_paths": state.PossiblePaths, "paths_complete": state.PathsComplete, "unverified": len(state.PossiblePaths) > 0,
	}
	r.bus.Publish(events.New(events.RunAborted, s.ID, runID, data))
	encoded, _ := json.MarshalIndent(data, "", "  ")
	content := harnessAbortRecordPrefix + "\n" + string(encoded)
	message := events.Message{ID: r.id("m"), Role: "system", Content: content, Category: "history", Tokens: int(math.Ceil(float64(len([]rune(content))) / 3.6)), Estimated: true, Turn: state.Turn}
	s.Append(message)
	r.bus.Publish(events.New(events.MessageAppended, s.ID, runID, map[string]any{"message": message}))
	return detail
}

func isHarnessAbortRecord(message events.Message) bool {
	return message.Role == "system" && message.Category == "history" && strings.HasPrefix(message.Content, harnessAbortRecordPrefix+"\n")
}

func harnessAbortContext(records []events.Message) string {
	parts := []string{}
	for _, message := range records {
		if isHarnessAbortRecord(message) {
			parts = append(parts, message.Content)
		}
	}
	return strings.Join(parts, "\n\n")
}

func appendHarnessAbortContext(system, context string) string {
	if context == "" {
		return system
	}
	return system + "\n\n" + context
}

func (r *Runner) stopped(s *session.Session, runID string, turn int, detail string) (string, string, int) {
	reason := r.abortReason(s.ID, runID)
	if strings.TrimSpace(detail) == "" {
		detail = "cancellation requested"
	}
	return reason, r.recordAbort(s, runID, reason, detail), turn
}

func abortDetail(boundExceeded bool) string {
	if boundExceeded {
		return fmt.Sprintf("run terminated after cancellation did not yield within %s; late completion fenced", cancellationBound)
	}
	return "cancellation requested"
}
