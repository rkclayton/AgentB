package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/llm"
	"harness/internal/session"
	"harness/internal/tools"
)

func TestActiveRunMessagesQueueAtZeroDepthAndDispatchInOrderAfterRunEnd(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n"))
	}))
	defer model.Close()
	workspace := t.TempDir()
	cfg := config.Defaults(workspace)
	cfg.Context.Accounting = "estimated"
	cfg.Run.QueueDepth = 0
	cfg.Run.MaxConcurrent = 1
	profile := cfg.Servers[0]
	profile.BaseURL = model.URL
	profile.RequestTimeoutS = 2
	profile.Context.NCtx = 32768
	profile.Context.ReserveOutput = 8192
	profile.Capabilities.Streaming = true
	profile.Capabilities.ToolCalls = true
	profile.Capabilities.OverflowBehavior = "error"
	profile.Capabilities.Tokenize = false
	cfg.Servers[0] = profile
	bus := events.NewBus()
	var eventMu sync.Mutex
	var recorded []events.Event
	bus.SetSink(func(event events.Event) error {
		eventMu.Lock()
		recorded = append(recorded, event)
		eventMu.Unlock()
		return nil
	})
	writers, err := events.NewWriters(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer writers.Close()
	profileLookup := func(id string) (*config.Profile, bool) { return &profile, id == profile.ID }
	registry := session.NewRegistry(bus, writers, profileLookup, cfg.Run.MaxTurns, func() config.Config { return cfg })
	item, err := registry.Create("main", profile.ID, workspace)
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(bus, tools.New(), &PromptRenderer{text: "system"}, profileLookup, func() config.Config { return cfg })
	scheduler := NewScheduler(runner, registry, bus, func() config.Config { return cfg })
	item.SetRun(session.RunState{Status: "running", RunID: "r0", MaxTurns: cfg.Run.MaxTurns})
	scheduler.active[item.ID] = &activeRun{}
	first, err := scheduler.Submit(context.Background(), item.ID, "first queued")
	if err != nil || !first.Queued || first.Position != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := scheduler.Submit(context.Background(), item.ID, "second queued")
	if err != nil || !second.Queued || second.Position != 2 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	scheduler.finish(queuedRun{s: item, runID: "r0"}, "done", "", 1)
	deadline := time.Now().Add(5 * time.Second)
	for (scheduler.Active(item.ID) || item.Snapshot().QueuedMessages != 0) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if scheduler.Active(item.ID) || item.Snapshot().QueuedMessages != 0 {
		t.Fatalf("queue did not drain: run=%+v queued=%d", item.Snapshot().Run, item.Snapshot().QueuedMessages)
	}
	messages := item.MessagesCopy()
	var users []string
	for _, message := range messages {
		if message.Role == "user" {
			users = append(users, message.Content)
		}
	}
	if len(users) != 2 || users[0] != "first queued" || users[1] != "second queued" {
		t.Fatalf("user dispatch order=%v", users)
	}
	eventMu.Lock()
	defer eventMu.Unlock()
	queued := 0
	for _, event := range recorded {
		if event.Type == events.MessageQueued {
			queued++
		}
	}
	if queued != 2 {
		t.Fatalf("message.queued count=%d", queued)
	}
}

func TestStopHoldsQueuedMessagesUntilNextExplicitSubmit(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n"))
	}))
	defer model.Close()
	cfg, profile, item, scheduler := schedulerFixture(t, model.URL)
	item.SetRun(session.RunState{Status: "running", RunID: "r0", MaxTurns: cfg.Run.MaxTurns})
	scheduler.active[item.ID] = &activeRun{cancel: func() {}}
	if result, err := scheduler.Submit(context.Background(), item.ID, "first queued"); err != nil || result.Position != 1 {
		t.Fatalf("first=%+v err=%v", result, err)
	}
	if result, err := scheduler.Submit(context.Background(), item.ID, "second queued"); err != nil || result.Position != 2 {
		t.Fatalf("second=%+v err=%v", result, err)
	}
	scheduler.Stop(item.ID, false)
	scheduler.finish(queuedRun{s: item, runID: "r0"}, "user_stop", "", 1)
	if snapshot := item.Snapshot(); snapshot.Run.Status != "held" || snapshot.QueuedMessages != 2 {
		t.Fatalf("held snapshot=%+v", snapshot)
	}
	if len(item.MessagesCopy()) != 0 {
		t.Fatalf("held messages dispatched=%+v", item.MessagesCopy())
	}
	if _, err := scheduler.Submit(context.Background(), item.ID, "resume queue"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for (scheduler.Active(item.ID) || item.Snapshot().QueuedMessages != 0) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	var users []string
	for _, message := range item.MessagesCopy() {
		if message.Role == "user" {
			users = append(users, message.Content)
		}
	}
	if len(users) != 3 || users[0] != "first queued" || users[1] != "second queued" || users[2] != "resume queue" {
		t.Fatalf("dispatch order=%v profile=%s", users, profile.ID)
	}
}

func TestModelUnreachableHoldsQueueDeduplicatesAndProbeReleases(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n"))
	}))
	defer model.Close()
	cfg, profile, item, scheduler := schedulerFixture(t, model.URL)
	item.SetRun(session.RunState{Status: "running", RunID: "r0", MaxTurns: cfg.Run.MaxTurns})
	scheduler.active[item.ID] = &activeRun{}
	first, err := scheduler.Submit(context.Background(), item.ID, "same pending bytes")
	if err != nil || first.Position != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := scheduler.Submit(context.Background(), item.ID, "same pending bytes")
	if err != nil || second.Position != 1 || item.Snapshot().QueuedMessages != 1 {
		t.Fatalf("dedupe second=%+v queued=%d err=%v", second, item.Snapshot().QueuedMessages, err)
	}
	scheduler.finish(queuedRun{s: item, runID: "r0"}, "model_unreachable", "dial tcp timeout", 1)
	if snapshot := item.Snapshot(); snapshot.Run.Status != "held" || snapshot.QueuedMessages != 1 {
		t.Fatalf("held=%+v", snapshot)
	}
	if scheduler.Active(item.ID) {
		t.Fatal("unreachable queue dispatched before probe")
	}
	scheduler.ReleaseModel(profile.ID)
	deadline := time.Now().Add(3 * time.Second)
	for (scheduler.Active(item.ID) || item.Snapshot().QueuedMessages != 0) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if scheduler.Active(item.ID) || item.Snapshot().QueuedMessages != 0 {
		t.Fatalf("queue not released: %+v", item.Snapshot())
	}
}

func TestLongRunSlowAccountingCompletesEstimatedAndCompacts(t *testing.T) {
	var slow atomic.Bool
	var chatCalls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/apply-template":
			var body struct {
				Messages []llm.Message `json:"messages"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			var prompt strings.Builder
			for _, message := range body.Messages {
				prompt.WriteString(messageText(message.Content))
				prompt.WriteString(message.ReasoningContent)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"prompt": prompt.String()})
		case "/tokenize":
			var body struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if len(body.Content) > 1000 && strings.Contains(body.Content, "busy") && slow.CompareAndSwap(true, false) {
				time.Sleep(3200 * time.Millisecond)
			}
			tokens := make([]int, max(1, len(body.Content)/4))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"tokens": tokens})
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			if chatCalls.Add(1) == 1 {
				_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-missing\",\"type\":\"function\",\"function\":{\"name\":\"missing\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":3000,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n"))
				return
			}
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n"))
		default:
			http.NotFound(w, request)
		}
	}))
	defer model.Close()
	_, profile, item, scheduler := schedulerFixture(t, model.URL)
	profile.RequestTimeoutS = 3
	profile.Context.NCtx, profile.Context.ReserveOutput = 8192, 1024
	profile.Capabilities.Tokenize = true
	profile.Capabilities.ApplyTemplate = true
	profile.Capabilities.ApplyTemplateTools = true
	scheduler.runner.cfg = func() config.Config {
		cfg := config.Defaults(item.Workspace)
		cfg.Context.Accounting = "exact"
		cfg.Servers[0] = *profile
		return cfg
	}
	for index := 0; index < 8; index++ {
		callID := fmt.Sprintf("old-call-%d", index)
		item.Append(events.Message{ID: fmt.Sprintf("old-assistant-%d", index), Role: "assistant", Category: "history", Tokens: 10, Turn: index + 1, ToolCalls: []events.ToolCall{{ID: callID, Name: "read_file", Arguments: `{"path":"old.txt","offset":1,"limit":1000}`}}})
		item.Append(events.Message{ID: fmt.Sprintf("old-result-%d", index), Role: "tool", Name: "read_file", ToolCallID: callID, Category: "files", Content: strings.Repeat("old result ", 400), Tokens: 1300, Turn: index + 1})
	}
	eventsSeen, unsubscribe := scheduler.bus.Subscribe()
	defer unsubscribe()
	slow.Store(true)
	started := time.Now()
	if _, err := scheduler.Submit(context.Background(), item.ID, "busy"); err != nil {
		t.Fatal(err)
	}
	var sawBusy, sawEstimated, sawCompaction bool
	deadline := time.After(12 * time.Second)
	for {
		select {
		case event := <-eventsSeen:
			switch event.Type {
			case events.ModelBusy:
				sawBusy = true
			case events.BudgetEvent:
				if budget, ok := event.Data.(events.Budget); ok && budget.Estimated {
					sawEstimated = true
				}
			case events.Compaction:
				sawCompaction = true
			case events.RunStopped:
				goto stopped
			}
		case <-deadline:
			t.Fatalf("slow-accounting run timed out: %+v", item.Snapshot().Run)
		}
	}

stopped:
	if elapsed := time.Since(started); elapsed < 3*time.Second {
		t.Fatalf("slow /tokenize elapsed=%s, want over 3s", elapsed)
	}
	if snapshot := item.Snapshot(); snapshot.Run.LastStopReason != "done" || !sawBusy || !sawEstimated || !sawCompaction || snapshot.CompactionCount == 0 {
		t.Fatalf("run=%+v busy=%t estimated=%t compaction=%t count=%d", snapshot.Run, sawBusy, sawEstimated, sawCompaction, snapshot.CompactionCount)
	}
}

func TestAccountingDialFailureFastStopsAsModelUnreachable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	_, profile, item, scheduler := schedulerFixture(t, "http://"+address)
	profile.Capabilities.Tokenize = true
	profile.Capabilities.ApplyTemplate = true
	profile.Capabilities.ApplyTemplateTools = true
	scheduler.runner.cfg = func() config.Config {
		cfg := config.Defaults(item.Workspace)
		cfg.Context.Accounting = "exact"
		cfg.Servers[0] = *profile
		return cfg
	}
	start := time.Now()
	if _, err := scheduler.Submit(context.Background(), item.ID, "unreachable"); err != nil {
		t.Fatal(err)
	}
	for scheduler.Active(item.ID) && time.Since(start) < 3*time.Second {
		time.Sleep(time.Millisecond)
	}
	if elapsed := time.Since(start); scheduler.Active(item.ID) || elapsed >= 3*time.Second {
		t.Fatalf("elapsed=%s active=%t", elapsed, scheduler.Active(item.ID))
	}
	if reason := item.Snapshot().Run.LastStopReason; reason != "model_unreachable" {
		t.Fatalf("reason=%q", reason)
	}
}

func TestStopCancelsBlackholedApplyTemplateInUnderOneSecond(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	blackhole := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/apply-template" {
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-request.Context().Done():
			case <-release:
			}
			return
		}
		if request.URL.Path == "/tokenize" {
			_, _ = w.Write([]byte(`{"tokens":[1]}`))
			return
		}
		http.NotFound(w, request)
	}))
	defer blackhole.Close()
	defer close(release)
	_, profile, item, scheduler := schedulerFixture(t, blackhole.URL)
	profile.Capabilities.Tokenize = true
	profile.Capabilities.ApplyTemplate = true
	profile.Capabilities.ApplyTemplateTools = true
	scheduler.runner.cfg = func() config.Config {
		cfg := config.Defaults(item.Workspace)
		cfg.Context.Accounting = "exact"
		cfg.Servers[0] = *profile
		return cfg
	}
	if _, err := scheduler.Submit(context.Background(), item.ID, "blocked request"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("apply-template was not reached")
	}
	started := time.Now()
	scheduler.Stop(item.ID, false)
	for scheduler.Active(item.ID) && time.Since(started) < time.Second {
		time.Sleep(time.Millisecond)
	}
	if elapsed := time.Since(started); scheduler.Active(item.ID) || elapsed >= time.Second {
		t.Fatalf("stop elapsed=%s active=%t", elapsed, scheduler.Active(item.ID))
	}
	if reason := item.Snapshot().Run.LastStopReason; reason != "safe" {
		t.Fatalf("stop reason=%q", reason)
	}
}

func TestSecondStopEscalatesToEmergency(t *testing.T) {
	_, _, item, scheduler := schedulerFixture(t, "http://127.0.0.1:1")
	item.SetRun(session.RunState{Status: "running", RunID: "r1", MaxTurns: 40})
	scheduler.active[item.ID] = &activeRun{cancel: func() {}}
	eventStream, unsubscribe := scheduler.bus.Subscribe()
	defer unsubscribe()
	scheduler.Stop(item.ID, false)
	scheduler.Stop(item.ID, false)
	if got := scheduler.active[item.ID].stopReason; got != "emergency" {
		t.Fatalf("stop reason=%q", got)
	}
	for index, reason := range []string{"safe", "emergency"} {
		select {
		case event := <-eventStream:
			if event.Type != events.RunStopping || event.RunID != "r1" || event.Data.(map[string]any)["reason"] != reason {
				t.Fatalf("event %d=%+v", index, event)
			}
		case <-time.After(time.Second):
			t.Fatalf("missing %s stop event", reason)
		}
	}
}

func schedulerFixture(t *testing.T, modelURL string) (config.Config, *config.Profile, *session.Session, *Scheduler) {
	t.Helper()
	workspace := t.TempDir()
	cfg := config.Defaults(workspace)
	cfg.Context.Accounting = "estimated"
	cfg.Run.MaxConcurrent = 1
	profile := cfg.Servers[0]
	profile.BaseURL, profile.Model, profile.RequestTimeoutS = modelURL, "model", 30
	profile.Context.NCtx, profile.Context.ReserveOutput = 32768, 8192
	profile.Capabilities.Streaming, profile.Capabilities.ToolCalls, profile.Capabilities.OverflowBehavior = true, true, "error"
	cfg.Servers[0] = profile
	bus := events.NewBus()
	writers, err := events.NewWriters(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writers.Close() })
	lookup := func(id string) (*config.Profile, bool) { return &profile, id == profile.ID }
	registry := session.NewRegistry(bus, writers, lookup, cfg.Run.MaxTurns, func() config.Config { return cfg })
	item, err := registry.Create("main", profile.ID, workspace)
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(bus, tools.New(), &PromptRenderer{text: "system {{tools}} {{memory}}"}, lookup, func() config.Config { return cfg })
	return cfg, &profile, item, NewScheduler(runner, registry, bus, func() config.Config { return cfg })
}
