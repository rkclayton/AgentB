package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/llm"
	"harness/internal/session"
	"harness/internal/tools"
)

func TestNearOutputFloorCompactsBeforeGeneration(t *testing.T) {
	var maxTokens atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, request)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		maxTokens.Store(int32(body["max_tokens"].(float64)))
		writeStreamChunk(t, w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "done"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 1000, "completion_tokens": 1}})
	}))
	defer model.Close()

	cfg := config.Defaults(t.TempDir())
	cfg.Context.Accounting = "estimated"
	profile := cfg.Servers[0]
	profile.ID, profile.BaseURL, profile.Model = "main", model.URL, "fake"
	profile.Context.NCtx, profile.Context.ReserveOutput = 12500, 4096
	profile.Capabilities.Streaming, profile.Capabilities.ToolCalls = true, true
	cfg.Servers = []config.Profile{profile}
	lookup := func(id string) (*config.Profile, bool) { return &profile, id == profile.ID }
	bus := newCapturedBus()
	runner := NewRunner(bus.Bus, tools.New(), &PromptRenderer{text: "system"}, lookup, func() config.Config { return cfg })
	item := &session.Session{ID: "main", ServerID: "main", Workspace: t.TempDir(), Runnable: true, ToolsEnabled: map[string]bool{}, ToolCalls: map[string]int{}, SchemaTokens: map[string]int{}, MarginalTokens: map[string]int{}}
	for index := 0; index < 8; index++ {
		callID := fmt.Sprintf("call-old-%d", index)
		item.Append(events.Message{ID: fmt.Sprintf("a-old-%d", index), Role: "assistant", Category: "history", Turn: index + 1, ToolCalls: []events.ToolCall{{ID: callID, Name: "read_file", Arguments: fmt.Sprintf(`{"path":"old-%d.txt"}`, index)}}})
		item.Append(events.Message{ID: fmt.Sprintf("t-old-%d", index), Role: "tool", Name: "read_file", ToolCallID: callID, Category: "files", Content: strings.Repeat("old output ", 600), Turn: index + 1})
	}
	item.Append(events.Message{ID: "u-new", Role: "user", Category: "history", Content: "continue", Turn: 0})

	reason, detail, _ := runner.Run(context.Background(), item, "r1")
	if reason != "done" || detail != "" {
		t.Fatalf("run=(%q,%q)", reason, detail)
	}
	if got := int(maxTokens.Load()); got < minimumOutputFloor {
		t.Fatalf("generation max_tokens=%d, floor=%d", got, minimumOutputFloor)
	}
	compactionIndex, requestIndex := -1, -1
	for index, event := range bus.Recent(item.ID) {
		if event.Type == events.Compaction && compactionIndex < 0 {
			compactionIndex = index
		}
		if event.Type == events.ModelRequest && requestIndex < 0 {
			requestIndex = index
		}
	}
	if compactionIndex < 0 || requestIndex < 0 || compactionIndex > requestIndex {
		t.Fatalf("compaction index=%d request index=%d", compactionIndex, requestIndex)
	}
}

func TestTruncatedToolRetryIsBoundedVisibleAndNeverWritesHarnessUserJSONL(t *testing.T) {
	var streamCalls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, request)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if stream, _ := body["stream"].(bool); !stream {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "Retry test"}, "finish_reason": "stop"}}})
			return
		}
		switch streamCalls.Add(1) {
		case 1:
			writeStreamChunk(t, w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "cut", "type": "function", "function": map[string]any{"name": "write_file", "arguments": `{"path":"x.txt","content":"cut`}}}}, "finish_reason": "length"}}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 4096}})
		case 2:
			choice, _ := body["tool_choice"].(map[string]any)
			function, _ := choice["function"].(map[string]any)
			if function["name"] != "write_file" {
				t.Fatalf("forced retry=%#v", body["tool_choice"])
			}
			writeStreamChunk(t, w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "good", "type": "function", "function": map[string]any{"name": "write_file", "arguments": `{"path":"x.txt","content":"ok"}`}}}}, "finish_reason": "tool_calls"}}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20}})
		default:
			writeStreamChunk(t, w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "done"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 1}})
		}
	}))
	defer model.Close()

	root := t.TempDir()
	writers, err := events.NewWriters(root)
	if err != nil {
		t.Fatal(err)
	}
	logPath, err := writers.OpenSession("main")
	if err != nil {
		t.Fatal(err)
	}
	bus := newCapturedBus()
	bus.SetSink(func(event events.Event) error {
		bus.mu.Lock()
		bus.values = append(bus.values, event)
		bus.mu.Unlock()
		return writers.Write(event)
	})
	cfg := config.Defaults(root)
	cfg.Context.Accounting = "estimated"
	profile := cfg.Servers[0]
	profile.ID, profile.BaseURL, profile.Model = "main", model.URL, "fake"
	profile.Context.NCtx, profile.Context.ReserveOutput = 32768, 10240
	profile.Capabilities.Streaming, profile.Capabilities.ToolCalls = true, true
	cfg.Servers = []config.Profile{profile}
	lookup := func(id string) (*config.Profile, bool) { return &profile, id == profile.ID }
	runner := NewRunner(bus.Bus, tools.New(&retryWriteTool{}), &PromptRenderer{text: "system {{tools}}"}, lookup, func() config.Config { return cfg })
	item := &session.Session{ID: "main", ServerID: "main", Workspace: root, Runnable: true, ToolsEnabled: map[string]bool{"write_file": true}, ToolCalls: map[string]int{}, SchemaTokens: map[string]int{}, MarginalTokens: map[string]int{}}
	if _, err := runner.AddUser(context.Background(), item, "operator text"); err != nil {
		t.Fatal(err)
	}
	reason, detail, _ := runner.Run(context.Background(), item, "r1")
	if reason != "done" || detail != "" || streamCalls.Load() != 3 {
		t.Fatalf("run=(%q,%q) stream calls=%d", reason, detail, streamCalls.Load())
	}
	if err := writers.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	users, retries := []string{}, 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var event events.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == events.ModelRetry {
			retries++
		}
		if event.Type != events.MessageAppended {
			continue
		}
		var wrapper struct {
			Message events.Message `json:"message"`
		}
		raw, _ := json.Marshal(event.Data)
		_ = json.Unmarshal(raw, &wrapper)
		if wrapper.Message.Role == "user" {
			users = append(users, wrapper.Message.Content)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if retries != 1 || len(users) != 1 || users[0] != "operator text" {
		t.Fatalf("JSONL retries=%d users=%q", retries, users)
	}
}

func TestStopBlockedModelResumesWithAbortRecord(t *testing.T) {
	for _, test := range []struct {
		name            string
		rejectMidSystem bool
	}{
		{name: "position-constrained", rejectMidSystem: true},
		{name: "unconstrained"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testStopBlockedModelResumesWithAbortRecord(t, test.rejectMidSystem)
		})
	}
}

func testStopBlockedModelResumesWithAbortRecord(t *testing.T, rejectMidSystem bool) {
	entered := make(chan struct{})
	resumed := make(chan []llm.Message, 1)
	var calls atomic.Int32
	var rejected atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/tokenize" {
			_, _ = fmt.Fprint(w, `{"tokens":[1,2,3,4]}`)
			return
		}
		var body struct {
			Messages []llm.Message `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if request.URL.Path == "/apply-template" {
			historyStarted := false
			for index, message := range body.Messages {
				system := message.Role == "system" || message.Role == "developer"
				if rejectMidSystem && index > 0 && system && historyStarted {
					rejected.Add(1)
					http.Error(w, "System message must be at the beginning.", http.StatusInternalServerError)
					return
				}
				if !system {
					historyStarted = true
				}
			}
			_, _ = fmt.Fprint(w, `{"prompt":"ok"}`)
			return
		}
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial model output\"}}]}\n\n")
			w.(http.Flusher).Flush()
			close(entered)
			<-request.Context().Done()
			return
		}
		resumed <- body.Messages
		writeStreamChunk(t, w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "resumed"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 1}})
	}))
	defer model.Close()
	_, _, item, scheduler := schedulerFixtureAccounting(t, model.URL, "exact")
	eventStream, unsubscribe := scheduler.bus.Subscribe()
	defer unsubscribe()
	if _, err := scheduler.Submit(context.Background(), item.ID, "blocked"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked model was not entered")
	}
	deltaSeen := false
	deadline := time.After(2 * time.Second)
	for !deltaSeen {
		select {
		case event := <-eventStream:
			data, _ := event.Data.(map[string]any)
			deltaSeen = event.Type == events.ModelDelta && data["kind"] == "content" && data["text"] == "partial model output"
		case <-deadline:
			t.Fatal("partial model delta was not projected before stop")
		}
	}
	started := time.Now()
	scheduler.Stop(item.ID, false)
	if elapsed := time.Since(started); elapsed > cancellationBound+time.Second || scheduler.Active(item.ID) {
		t.Fatalf("stop elapsed=%s active=%t", elapsed, scheduler.Active(item.ID))
	}
	if item.Snapshot().Run.LastStopReason != "aborted_mid_model" {
		t.Fatalf("stopped run=%+v", item.Snapshot().Run)
	}
	if _, err := scheduler.Submit(context.Background(), item.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	var messages []llm.Message
	select {
	case messages = <-resumed:
	case <-time.After(3 * time.Second):
		t.Fatalf("resuming model request missing: rejected=%d run=%+v", rejected.Load(), item.Snapshot().Run)
	}
	joined := ""
	historyStarted := false
	var sentAbort llm.Message
	for index, message := range messages {
		joined += fmt.Sprintf("%s:%s\n", message.Role, message.Content)
		system := message.Role == "system" || message.Role == "developer"
		if index > 0 && system && historyStarted {
			t.Fatalf("in-position harness message=%+v", message)
		}
		if system && strings.HasPrefix(messageText(message.Content), harnessAbortRecordPrefix+"\n") {
			sentAbort = message
		}
		if !system {
			historyStarted = true
		}
	}
	stored := item.MessagesCopy()
	var abortRecord events.Message
	for _, message := range stored {
		if strings.HasPrefix(message.Content, harnessAbortRecordPrefix+"\n") {
			abortRecord = message
			break
		}
	}
	if rejected.Load() != 0 || len(messages) == 0 || messages[0].Role != "system" || sentAbort.Role != "system" || messageText(sentAbort.Content) != abortRecord.Content || abortRecord.Role != "system" || !strings.Contains(joined, "partial model output") || !strings.Contains(joined, "user:resume") {
		t.Fatalf("resuming messages:\n%s", joined)
	}
}

func TestAbortRecordKeepsWriteEvidenceAndRedactsPartialServiceHeaders(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	bus := newCapturedBus()
	runner := NewRunner(bus.Bus, tools.New(), &PromptRenderer{text: "system"}, cfg.Profile, func() config.Config { return cfg })
	item := &session.Session{ID: "main", Workspace: t.TempDir()}
	runner.beginFlight(item.ID, "r-write")
	runner.setFlightTool(item.ID, "r-write", 4, events.ToolCall{ID: "write", Name: "write_file", Arguments: `{"path":"out.txt","content":"partial"}`}, map[string]any{"path": "out.txt", "content": "partial"})
	runner.setFlightToolOutput(item.ID, "r-write", "partial tool output")
	runner.recordAbort(item, "r-write", "aborted_mid_tool", "test")
	messages := item.MessagesCopy()
	if len(messages) != 1 || !strings.Contains(messages[0].Content, "out.txt") || !strings.Contains(messages[0].Content, "partial tool output") {
		t.Fatalf("abort message=%s", messages[0].Content)
	}
	writeAbort := bus.Recent(item.ID)[0].Data.(map[string]any)
	tool := writeAbort["tool_in_flight"].(map[string]any)
	if tool["arguments"] != `{"path":"out.txt","content":"partial"}` || fmt.Sprint(writeAbort["possibly_written_paths"]) != "[out.txt]" || writeAbort["unverified"] != true {
		t.Fatalf("write abort=%#v", writeAbort)
	}

	runner.beginFlight(item.ID, "r-service")
	runner.setFlightStage(item.ID, "r-service", 2, "call_model")
	runner.addFlightDelta(item.ID, "r-service", llm.Delta{Kind: "tool_call", Index: 0, Name: "call_service", Text: `{"headers":{"Authorization":"secret"}}`})
	runner.recordAbort(item, "r-service", "aborted_mid_model", "test")
	last := item.MessagesCopy()[1].Content
	if strings.Contains(last, "secret") || !strings.Contains(last, "redacted: call_service arguments") {
		t.Fatalf("service abort record=%s", last)
	}
}
