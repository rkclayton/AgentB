package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
	"harness/internal/tools"
)

type retryWriteTool struct{}

func (*retryWriteTool) Name() string        { return "write_file" }
func (*retryWriteTool) Description() string { return "write a file" }
func (*retryWriteTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}}}
}
func (*retryWriteTool) Call(context.Context, *session.Session, map[string]any) (string, error) {
	return "wrote game.html", nil
}

func TestLengthDuringToolArgumentsDoesNotEnterToolHistory(t *testing.T) {
	var requests atomic.Int32
	var firstMaxTokens int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, request)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if stream, _ := body["stream"].(bool); !stream {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "Game edit"}, "finish_reason": "stop"}}})
			return
		}
		attempt := requests.Add(1)
		if attempt == 1 {
			firstMaxTokens = int(body["max_tokens"].(float64))
			delta := map[string]any{
				"content": "I chose the layout. ",
				"tool_calls": []any{map[string]any{
					"index": 0, "id": "call-1", "type": "function",
					"function": map[string]any{"name": "write_file", "arguments": `{"path":"game.html","content":"<!doctype`},
				}},
			}
			writeStreamChunk(t, w, map[string]any{
				"choices": []any{map[string]any{"delta": delta, "finish_reason": "length"}},
				"usage":   map[string]any{"prompt_tokens": 100, "completion_tokens": firstMaxTokens},
			})
			return
		}
		if attempt == 2 {
			choice, _ := body["tool_choice"].(map[string]any)
			function, _ := choice["function"].(map[string]any)
			if function["name"] != "write_file" {
				t.Fatalf("retry tool_choice=%#v", body["tool_choice"])
			}
			writeStreamChunk(t, w, map[string]any{
				"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "call-2", "type": "function", "function": map[string]any{"name": "write_file", "arguments": `{"path":"game.html","content":"done"}`},
				}}}, "finish_reason": "tool_calls"}},
				"usage": map[string]any{"prompt_tokens": 120, "completion_tokens": 20},
			})
			return
		}
		writeStreamChunk(t, w, map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{"content": "Done without retrying the call."}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 120, "completion_tokens": 10},
		})
	}))
	defer server.Close()

	runner, item, bus := truncationRunner(t, server.URL, "estimated")
	reason, detail, turns := runner.Run(context.Background(), item, "r1")
	if reason != "done" || detail != "" || turns != 3 {
		t.Fatalf("run=(%q,%q,%d)", reason, detail, turns)
	}
	if firstMaxTokens <= 0 {
		t.Fatalf("max_tokens=%d", firstMaxTokens)
	}
	messages := item.MessagesCopy()
	if len(messages) != 3 || messages[0].Role != "assistant" || len(messages[0].ToolCalls) != 1 || messages[0].ToolCalls[0].Name != "write_file" || messages[1].Role != "tool" || messages[2].Content != "Done without retrying the call." {
		t.Fatalf("messages=%#v", messages)
	}
	for _, message := range messages {
		if message.Role == "user" {
			t.Fatalf("harness wrote user message: %#v", message)
		}
	}
	retries, calls, results := 0, 0, 0
	for _, event := range bus.Recent(item.ID) {
		if event.Type == events.ModelRetry {
			retries++
		}
		if event.Type == events.ToolCallEvent {
			calls++
		}
		if event.Type == events.ToolResult {
			results++
		}
	}
	if retries != 1 || calls != 1 || results != 1 {
		t.Fatalf("retries=%d calls=%d results=%d", retries, calls, results)
	}
}

func TestMalformedHistoryRepairPreservesFailureNoteWithoutRestoringFailedAction(t *testing.T) {
	var rejected atomic.Int32
	var dispatched atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/apply-template":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(body["messages"])
			if strings.Contains(string(encoded), `call-bad`) && strings.Contains(string(encoded), `tool_calls`) {
				rejected.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"error":{"message":"Failed to parse tool call arguments as JSON: missing closing quote"}}`)
				return
			}
			data, _ := json.Marshal(map[string]string{"prompt": string(encoded)})
			_, _ = w.Write(data)
		case "/tokenize":
			fmt.Fprint(w, `{"tokens":[1,2,3,4]}`)
		case "/v1/chat/completions":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(body["messages"])
			dispatched.Store(string(encoded))
			writeStreamChunk(t, w, map[string]any{
				"choices": []any{map[string]any{"delta": map[string]any{"content": "Recovered."}, "finish_reason": "stop"}},
				"usage":   map[string]any{"prompt_tokens": 40, "completion_tokens": 2},
			})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	runner, item, bus := truncationRunner(t, server.URL, "exact")
	item.Append(events.Message{ID: "m-bad", Role: "assistant", Content: "I chose the game design.", Category: "history", Turn: 1, ToolCalls: []events.ToolCall{{ID: "call-bad", Name: "write_file", Arguments: `{"path":"game.html","content":"<!doctype`}}})
	failed := false
	item.Append(events.Message{ID: "m-result", Role: "tool", Content: "error: invalid JSON arguments", Category: "results", Turn: 1, ToolCallID: "call-bad", Name: "write_file", OK: &failed})
	item.Append(events.Message{ID: "m-later", Role: "user", Content: "please recover", Category: "history", Turn: 0})

	reason, detail, turns := runner.Run(context.Background(), item, "r2")
	if reason != "done" || detail != "" || turns != 1 {
		t.Fatalf("run=(%q,%q,%d)", reason, detail, turns)
	}
	if rejected.Load() != 1 {
		t.Fatalf("apply-template rejects=%d, want exactly one before repair", rejected.Load())
	}
	messages := item.MessagesCopy()
	if len(messages) != 3 || messages[0].ID != "m-bad" || len(messages[0].ToolCalls) != 0 || messages[1].ID != "m-later" {
		t.Fatalf("repaired messages=%#v", messages)
	}
	if messages[2].Content != "Recovered." {
		t.Fatalf("final=%#v", messages[2])
	}
	for _, message := range messages {
		if len(message.ToolCalls) != 0 || message.ToolCallID == "call-bad" || strings.Contains(message.Content, "invalid JSON arguments") || strings.Contains(message.Content, "<!doctype") {
			t.Fatalf("failed action was restored to transcript: %#v", message)
		}
	}
	dispatchedMessages, _ := dispatched.Load().(string)
	if dispatchedMessages == "" || strings.Contains(dispatchedMessages, "call-bad") || strings.Contains(dispatchedMessages, "<!doctype") || strings.Contains(dispatchedMessages, "cut off") {
		t.Fatalf("dispatched transcript retained malformed history: %s", dispatchedMessages)
	}
	foundBody, foundRetry := false, false
	for _, event := range bus.Recent(item.ID) {
		if event.Type == events.Error && strings.Contains(event.Data.(map[string]any)["message"].(string), "missing closing quote") {
			foundBody = true
		}
		if event.Type == events.ModelRetry {
			foundRetry = true
		}
	}
	if !foundBody || !foundRetry {
		t.Fatalf("foundBody=%t foundRetry=%t", foundBody, foundRetry)
	}
}

func TestRequestTokenLimitUsesRemainingContextAfterReserve(t *testing.T) {
	profile := &config.Profile{Context: config.Context{NCtx: 32768, ReserveOutput: 10240}}
	budget := events.Budget{NCtx: 32768, Reserve: 10240, UsedEst: 8451, Mode: "exact"}
	if got := requestTokenLimit(profile, budget, guardedPromptTokens(budget)); got != 10240 {
		t.Fatalf("max_tokens=%d, want 10240", got)
	}
	budget.Mode = "estimated"
	if got := guardedPromptTokens(budget); got != 9297 {
		t.Fatalf("guarded prompt=%d, want 9297", got)
	}
	budget.UsedEst = 30000
	if got := requestTokenLimit(profile, budget, guardedPromptTokens(budget)); got != 0 {
		t.Fatalf("estimated exhausted max_tokens=%d, want 0 before compaction", got)
	}
	profile.Context.ReserveOutput = 1024
	budget.Mode, budget.UsedEst = "exact", 20000
	if got := requestTokenLimit(profile, budget, guardedPromptTokens(budget)); got != minimumOutputFloor {
		t.Fatalf("configured-low max_tokens=%d, want floor %d", got, minimumOutputFloor)
	}
}

func truncationRunner(t *testing.T, baseURL, accounting string) (*Runner, *session.Session, *capturedBus) {
	t.Helper()
	cfg := config.Defaults(t.TempDir())
	cfg.Context.Accounting = accounting
	profile := cfg.Servers[0]
	profile.ID, profile.Label, profile.BaseURL, profile.Model = "main", "main", baseURL, "test-model"
	profile.RequestTimeoutS = 5
	profile.Context.NCtx = 32768
	profile.Context.ReserveOutput = 10240
	profile.Capabilities.NCtx = 32768
	profile.Capabilities.Streaming = true
	profile.Capabilities.ToolCalls = true
	profile.Capabilities.OverflowBehavior = "error"
	profile.Capabilities.Tokenize = accounting == "exact"
	profile.Capabilities.ApplyTemplate = accounting == "exact"
	profile.Capabilities.ApplyTemplateTools = accounting == "exact"
	cfg.Servers = []config.Profile{profile}
	cfg.Agents = []config.Agent{{Name: "Main", B: "main", Toolset: config.FullToolset()}}
	bus := newCapturedBus()
	runner := NewRunner(bus.Bus, tools.New(&retryWriteTool{}), &PromptRenderer{text: "system {{workspace}} {{memory}} {{tools}}"}, cfg.Profile, func() config.Config { return cfg })
	enabled := map[string]bool{}
	for _, name := range config.FullToolset() {
		enabled[name] = true
	}
	item := &session.Session{ID: "main", ServerID: "main", Workspace: t.TempDir(), Run: session.RunState{Status: "running", MaxTurns: cfg.Run.MaxTurns}, Runnable: true, ToolsEnabled: enabled, ToolCalls: map[string]int{}, SchemaTokens: map[string]int{}, MarginalTokens: map[string]int{}, Budget: events.Budget{NCtx: 32768, Reserve: 10240}}
	return runner, item, bus
}

func writeStreamChunk(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
}
