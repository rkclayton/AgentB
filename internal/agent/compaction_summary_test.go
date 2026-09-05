package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
	"harness/internal/tools"
)

type summaryServer struct {
	server        *httptest.Server
	chatCalls     atomic.Int32
	templateCalls atomic.Int32
	tokenizeCalls atomic.Int32
	mu            sync.Mutex
	lastChat      map[string]any
	content       string
}

func newSummaryServer(t *testing.T, content string) *summaryServer {
	t.Helper()
	result := &summaryServer{content: content}
	result.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/apply-template":
			result.templateCalls.Add(1)
			var body struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			var prompt strings.Builder
			for _, message := range body.Messages {
				prompt.WriteString("<message>")
				prompt.WriteString(message.Content)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"prompt": prompt.String()})
		case "/tokenize":
			result.tokenizeCalls.Add(1)
			var body struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			count := max(1, len([]rune(body.Content))/4)
			_ = json.NewEncoder(w).Encode(map[string]any{"tokens": make([]int, count)})
		case "/v1/chat/completions":
			result.chatCalls.Add(1)
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			result.mu.Lock()
			result.lastChat = body
			result.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": result.content}, "finish_reason": "stop"}},
				"usage":   map[string]any{"prompt_tokens": 111, "completion_tokens": 22, "prompt_tokens_details": map[string]any{"cached_tokens": 33}},
			})
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(result.server.Close)
	return result
}

func TestCompactionAuxUnsetUsesOneMainCall(t *testing.T) {
	mainServer := newSummaryServer(t, "short summary")
	runner, item, bus, _ := compactionRunner(t, mainServer, nil, 32768)
	if !runner.summarize(context.Background(), item, "run", profileForRunner(runner, "main")) {
		t.Fatal("summary was not accepted")
	}
	if mainServer.chatCalls.Load() != 1 || mainServer.templateCalls.Load() != 0 {
		t.Fatalf("main chat=%d template=%d", mainServer.chatCalls.Load(), mainServer.templateCalls.Load())
	}
	mainServer.mu.Lock()
	body := mainServer.lastChat
	mainServer.mu.Unlock()
	if body["max_tokens"] != float64(compactionMaxTokens) || body["temperature"] != .3 {
		t.Fatalf("request params=%v", body)
	}
	attempt, compact := compactionEvents(t, bus, item.ID)
	if attempt.Role != "main" || attempt.ProfileID != "main" || attempt.Outcome != "accepted" || compact["profile_id"] != "main" {
		t.Fatalf("attempt=%+v compact=%v", attempt, compact)
	}
	snapshot := item.Snapshot(nil)
	if snapshot.CompactionModelCalls != 1 || snapshot.CompactionPrompt != 111 || snapshot.CompactionCompletion != 22 {
		t.Fatalf("ledger=%+v", snapshot)
	}
}

func TestCompactionUsesFittingAuxProfile(t *testing.T) {
	mainServer := newSummaryServer(t, "main summary")
	auxServer := newSummaryServer(t, "aux summary")
	runner, item, bus, _ := compactionRunner(t, mainServer, auxServer, 32768)
	if !runner.summarize(context.Background(), item, "run", profileForRunner(runner, "main")) {
		t.Fatal("aux summary was not accepted")
	}
	if auxServer.chatCalls.Load() != 1 || auxServer.templateCalls.Load() != 1 || auxServer.tokenizeCalls.Load() != 1 || mainServer.chatCalls.Load() != 0 {
		t.Fatalf("aux chat/template/tokenize=%d/%d/%d main chat=%d", auxServer.chatCalls.Load(), auxServer.templateCalls.Load(), auxServer.tokenizeCalls.Load(), mainServer.chatCalls.Load())
	}
	attempt, compact := compactionEvents(t, bus, item.ID)
	if attempt.Role != "aux" || attempt.ProfileID != "aux" || attempt.Estimated || compact["profile_id"] != "aux" {
		t.Fatalf("attempt=%+v compact=%v", attempt, compact)
	}
}

func TestCompactionSkipsSmallAuxAndFallsBackToMain(t *testing.T) {
	mainServer := newSummaryServer(t, "main summary")
	auxServer := newSummaryServer(t, "aux summary")
	runner, item, bus, _ := compactionRunner(t, mainServer, auxServer, 100)
	if !runner.summarize(context.Background(), item, "run", profileForRunner(runner, "main")) {
		t.Fatal("main fallback summary was not accepted")
	}
	if auxServer.chatCalls.Load() != 0 || mainServer.chatCalls.Load() != 1 {
		t.Fatalf("aux chat=%d main chat=%d", auxServer.chatCalls.Load(), mainServer.chatCalls.Load())
	}
	attempts := summaryAttempts(bus, item.ID)
	if len(attempts) != 2 || attempts[0].Outcome != "skipped" || attempts[0].Dispatched || attempts[1].FallbackReason != "aux_context" {
		t.Fatalf("attempts=%+v", attempts)
	}
}

func TestCompactionAuxErrorFallsBackToMain(t *testing.T) {
	mainServer := newSummaryServer(t, "main summary")
	runner, item, bus, cfg := compactionRunner(t, mainServer, nil, 32768)
	aux := cfg.Servers[0]
	aux.ID, aux.Label, aux.BaseURL, aux.Model = "aux", "aux", "http://127.0.0.1:1", "offline"
	aux.RequestTimeoutS = 1
	cfg.Servers = append(cfg.Servers, aux)
	cfg.Roles.Aux = "aux"
	if !runner.summarize(context.Background(), item, "run", profileForRunner(runner, "main")) {
		t.Fatal("main fallback summary was not accepted")
	}
	if mainServer.chatCalls.Load() != 1 {
		t.Fatalf("main chat=%d", mainServer.chatCalls.Load())
	}
	attempts := summaryAttempts(bus, item.ID)
	if len(attempts) != 2 || attempts[0].Outcome != "error" || !attempts[0].Dispatched || attempts[1].FallbackReason != "aux_error" {
		t.Fatalf("attempts=%+v", attempts)
	}
	snapshot := item.Snapshot(nil)
	if snapshot.CompactionModelCalls != 2 || snapshot.CompactionPrompt != 111 || snapshot.CompactionCompletion != 22 {
		t.Fatalf("ledger=%+v", snapshot)
	}
}

func TestCompactionAuxFitCheckErrorFallsBackBeforeDispatch(t *testing.T) {
	mainServer := newSummaryServer(t, "main summary")
	auxServer := newSummaryServer(t, "aux summary")
	runner, item, bus, _ := compactionRunner(t, mainServer, auxServer, 32768)
	auxServer.server.Close()
	if !runner.summarize(context.Background(), item, "run", profileForRunner(runner, "main")) {
		t.Fatal("main fallback summary was not accepted")
	}
	if auxServer.chatCalls.Load() != 0 || mainServer.chatCalls.Load() != 1 {
		t.Fatalf("aux chat=%d main chat=%d", auxServer.chatCalls.Load(), mainServer.chatCalls.Load())
	}
	attempts := summaryAttempts(bus, item.ID)
	if len(attempts) != 2 || attempts[0].Outcome != "error" || attempts[0].Dispatched || attempts[0].Estimated || attempts[1].FallbackReason != "aux_fit_error" {
		t.Fatalf("attempts=%+v", attempts)
	}
	snapshot := item.Snapshot(nil)
	if snapshot.CompactionModelCalls != 1 || snapshot.CompactionPrompt != 111 || snapshot.CompactionCompletion != 22 {
		t.Fatalf("ledger=%+v", snapshot)
	}
}

func TestCompactionMainFallbackFailureLeavesContextUntouched(t *testing.T) {
	mainServer := newSummaryServer(t, "main summary")
	auxServer := newSummaryServer(t, "aux summary")
	runner, item, bus, _ := compactionRunner(t, mainServer, auxServer, 32768)
	before := item.MessagesCopy()
	auxServer.server.Close()
	mainServer.server.Close()
	if runner.summarize(context.Background(), item, "run", profileForRunner(runner, "main")) {
		t.Fatal("failed main fallback reported an accepted summary")
	}
	if len(item.MessagesCopy()) != len(before) {
		t.Fatalf("messages changed after both profiles failed: before=%d after=%d", len(before), len(item.MessagesCopy()))
	}
	attempts := summaryAttempts(bus, item.ID)
	if len(attempts) != 2 || attempts[0].Outcome != "error" || attempts[0].Dispatched || attempts[1].Outcome != "error" || !attempts[1].Dispatched || attempts[1].FallbackReason != "aux_fit_error" {
		t.Fatalf("attempts=%+v", attempts)
	}
	foundOperationalError := false
	for _, event := range bus.Recent(item.ID) {
		if event.Type == events.Error {
			foundOperationalError = true
		}
	}
	if !foundOperationalError {
		t.Fatal("main fallback failure was not published as an operational error")
	}
}

func TestCompactionRejectedAuxFallsBackToMain(t *testing.T) {
	mainServer := newSummaryServer(t, "main summary")
	auxServer := newSummaryServer(t, strings.Repeat("large summary ", 1000))
	runner, item, bus, _ := compactionRunner(t, mainServer, auxServer, 32768)
	if !runner.summarize(context.Background(), item, "run", profileForRunner(runner, "main")) {
		t.Fatal("main fallback summary was not accepted")
	}
	if auxServer.chatCalls.Load() != 1 || mainServer.chatCalls.Load() != 1 {
		t.Fatalf("aux chat=%d main chat=%d", auxServer.chatCalls.Load(), mainServer.chatCalls.Load())
	}
	attempts := summaryAttempts(bus, item.ID)
	if len(attempts) != 2 || attempts[0].Outcome != "rejected" || attempts[1].FallbackReason != "aux_rejected" {
		t.Fatalf("attempts=%+v", attempts)
	}
}

func compactionRunner(t *testing.T, mainServer, auxServer *summaryServer, auxNCtx int) (*Runner, *session.Session, *events.Bus, *config.Config) {
	t.Helper()
	cfg := config.Defaults(t.TempDir())
	cfg.Context.Accounting = "auto"
	main := cfg.Servers[0]
	main.ID, main.Label, main.BaseURL, main.Model = "main", "main", mainServer.server.URL, "main-model"
	main.Context.NCtx = 32768
	main.RequestTimeoutS = 2
	main.Capabilities.Tokenize = false
	cfg.Servers = []config.Profile{main}
	cfg.Roles = config.Roles{Main: "main"}
	if auxServer != nil {
		aux := main
		aux.ID, aux.Label, aux.BaseURL, aux.Model = "aux", "aux", auxServer.server.URL, "aux-model"
		aux.Context.NCtx = auxNCtx
		aux.Capabilities.Tokenize = auxNCtx > 100
		aux.Capabilities.ApplyTemplate = auxNCtx > 100
		cfg.Servers = append(cfg.Servers, aux)
		cfg.Roles.Aux = "aux"
	}
	bus := events.NewBus()
	runner := NewRunner(bus, tools.New(), &PromptRenderer{text: "system {{workspace}} {{memory}} {{tools}}"}, cfg.Profile, func() config.Config { return cfg })
	item := &session.Session{ID: "main", ServerID: "main", Workspace: t.TempDir(), ToolsEnabled: map[string]bool{}, ToolCalls: map[string]int{}, SchemaTokens: map[string]int{}, MarginalTokens: map[string]int{}}
	for index := 0; index < 10; index++ {
		item.Append(events.Message{ID: fmt.Sprintf("m%d", index), Role: "user", Content: strings.Repeat("history ", 20), Category: "history", Tokens: 100})
	}
	return runner, item, bus, &cfg
}

func profileForRunner(runner *Runner, id string) *config.Profile {
	profile, _ := runner.profile(id)
	return profile
}

func summaryAttempts(bus *events.Bus, sessionID string) []events.CompactionSummaryData {
	result := []events.CompactionSummaryData{}
	for _, event := range bus.Recent(sessionID) {
		if event.Type == events.CompactionSummary {
			result = append(result, event.Data.(events.CompactionSummaryData))
		}
	}
	return result
}

func compactionEvents(t *testing.T, bus *events.Bus, sessionID string) (events.CompactionSummaryData, map[string]any) {
	t.Helper()
	attempts := summaryAttempts(bus, sessionID)
	if len(attempts) != 1 {
		t.Fatalf("summary attempts=%+v", attempts)
	}
	for _, event := range bus.Recent(sessionID) {
		if event.Type == events.Compaction {
			return attempts[0], event.Data.(map[string]any)
		}
	}
	t.Fatal("compaction event missing")
	return events.CompactionSummaryData{}, nil
}
