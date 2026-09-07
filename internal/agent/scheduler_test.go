package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
	"harness/internal/tools"
)

func TestActiveRunMessagesQueueAtZeroDepthAndDispatchInOrderAfterStop(t *testing.T) {
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
	scheduler.active[item.ID] = activeRun{}
	first, err := scheduler.Submit(context.Background(), item.ID, "first queued")
	if err != nil || !first.Queued || first.Position != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := scheduler.Submit(context.Background(), item.ID, "second queued")
	if err != nil || !second.Queued || second.Position != 2 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	scheduler.finish(queuedRun{s: item, runID: "r0"}, "user_stop", "", 1)
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
