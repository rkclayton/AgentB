package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"harness/internal/agent"
	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
	"harness/internal/tools"
)

type agentServerFixture struct {
	server    *Server
	scheduler *agent.Scheduler
	registry  *session.Registry
	session   *session.Session
	oldID     string
	newID     string
}

func TestAgentServerChangeWaitsForIdleThenUsesNewServer(t *testing.T) {
	oldStarted := make(chan struct{})
	releaseOld := make(chan struct{})
	var oldCalls atomic.Int64
	oldModel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oldCalls.Add(1)
		select {
		case <-oldStarted:
		default:
			close(oldStarted)
		}
		select {
		case <-releaseOld:
			writeModelDone(w)
		case <-r.Context().Done():
		}
	}))
	defer oldModel.Close()
	newStarted := make(chan struct{})
	var newCalls atomic.Int64
	newModel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		newCalls.Add(1)
		select {
		case <-newStarted:
		default:
			close(newStarted)
		}
		writeModelDone(w)
	}))
	defer newModel.Close()
	fixture := newAgentServerFixture(t, oldModel.URL, newModel.URL)

	if _, err := fixture.scheduler.Submit(context.Background(), fixture.session.ID, "first"); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, oldStarted, "first request did not reach original server")
	response := postAgentServer(t, fixture.server, fixture.session.AgentID, `{"action":"set","server_id":"new"}`)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"status":"pending"`) {
		t.Fatalf("pending status=%d body=%s", response.Code, response.Body)
	}
	if got := fixture.server.ConfigSnapshot().Agents[0].B; got != fixture.oldID {
		t.Fatalf("binding changed during run: %s", got)
	}
	if got := fixture.session.Snapshot().ServerID; got != fixture.oldID {
		t.Fatalf("session changed during run: %s", got)
	}

	close(releaseOld)
	waitFor(t, func() bool {
		return !fixture.scheduler.Active(fixture.session.ID) && fixture.server.ConfigSnapshot().Agents[0].B == fixture.newID
	}, "pending binding was not applied at idle")
	if got := fixture.session.Snapshot().ServerID; got != fixture.newID {
		t.Fatalf("session binding=%s, want %s", got, fixture.newID)
	}
	if _, err := fixture.scheduler.Submit(context.Background(), fixture.session.ID, "second"); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, newStarted, "next request did not reach new server")
	waitFor(t, func() bool { return !fixture.scheduler.Active(fixture.session.ID) }, "second run did not finish")
	if oldCalls.Load() != 1 || newCalls.Load() != 1 {
		t.Fatalf("model calls old=%d new=%d", oldCalls.Load(), newCalls.Load())
	}
}

func TestAgentServerChangeAppliesAfterStop(t *testing.T) {
	oldStarted := make(chan struct{})
	releaseOld := make(chan struct{})
	oldModel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-oldStarted:
		default:
			close(oldStarted)
		}
		select {
		case <-releaseOld:
			writeModelDone(w)
		case <-r.Context().Done():
		}
	}))
	defer oldModel.Close()
	newModel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeModelDone(w) }))
	defer newModel.Close()
	fixture := newAgentServerFixture(t, oldModel.URL, newModel.URL)

	if _, err := fixture.scheduler.Submit(context.Background(), fixture.session.ID, "stop me"); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, oldStarted, "request did not reach original server")
	if response := postAgentServer(t, fixture.server, fixture.session.AgentID, `{"action":"set","server_id":"new"}`); response.Code != http.StatusAccepted {
		t.Fatalf("pending status=%d body=%s", response.Code, response.Body)
	}
	eventStream, unsubscribe := fixture.server.bus.Subscribe()
	defer unsubscribe()
	stopDone := make(chan struct{})
	go func() {
		fixture.scheduler.Stop(fixture.session.ID, false)
		close(stopDone)
	}()
	waitEventType(t, eventStream, events.RunStopping, "run did not enter stopping state")
	close(releaseOld)
	waitSignal(t, stopDone, "STOP did not finish")
	waitFor(t, func() bool { return fixture.server.ConfigSnapshot().Agents[0].B == fixture.newID }, "STOP did not apply pending binding")
}

func TestAgentServerChangeCanBeCancelled(t *testing.T) {
	oldStarted := make(chan struct{})
	releaseOld := make(chan struct{})
	oldModel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-oldStarted:
		default:
			close(oldStarted)
		}
		select {
		case <-releaseOld:
			writeModelDone(w)
		case <-r.Context().Done():
		}
	}))
	defer oldModel.Close()
	newModel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeModelDone(w) }))
	defer newModel.Close()
	fixture := newAgentServerFixture(t, oldModel.URL, newModel.URL)

	if _, err := fixture.scheduler.Submit(context.Background(), fixture.session.ID, "keep old"); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, oldStarted, "request did not reach original server")
	if response := postAgentServer(t, fixture.server, fixture.session.AgentID, `{"action":"set","server_id":"new"}`); response.Code != http.StatusAccepted {
		t.Fatalf("pending status=%d body=%s", response.Code, response.Body)
	}
	if response := postAgentServer(t, fixture.server, fixture.session.AgentID, `{"action":"set","server_id":"old"}`); response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"status":"pending"`) {
		t.Fatalf("current selection must preserve pending change: status=%d body=%s", response.Code, response.Body)
	}
	if response := postAgentServer(t, fixture.server, fixture.session.AgentID, `{"action":"cancel"}`); response.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", response.Code, response.Body)
	}
	close(releaseOld)
	waitFor(t, func() bool { return !fixture.scheduler.Active(fixture.session.ID) }, "run did not finish")
	if got := fixture.server.ConfigSnapshot().Agents[0].B; got != fixture.oldID {
		t.Fatalf("cancelled binding changed to %s", got)
	}
}

func newAgentServerFixture(t *testing.T, oldURL, newURL string) agentServerFixture {
	t.Helper()
	root := t.TempDir()
	if evidenceRoot := os.Getenv("AGENTB_W4_EVIDENCE"); evidenceRoot != "" {
		root = filepath.Join(evidenceRoot, t.Name())
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Defaults(root)
	cfg.Context.Accounting = "estimated"
	cfg.Run.MaxConcurrent = 1
	oldProfile := runnableTestProfile("old")
	oldProfile.BaseURL, oldProfile.RequestTimeoutS = oldURL, 30
	newProfile := runnableTestProfile("new")
	newProfile.BaseURL, newProfile.RequestTimeoutS = newURL, 30
	cfg.Servers = []config.Profile{oldProfile, newProfile}
	cfg.Agents = []config.Agent{{Name: "Coder", B: oldProfile.ID, Toolset: config.FullToolset()}}
	configPath := filepath.Join(root, "harness.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	writers, err := events.NewWriters(filepath.Join(root, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	bus.SetSink(writers.Write)
	t.Cleanup(func() { _ = writers.Close() })
	server := New(&cfg, configPath, root, RuntimeRoots{Application: root, Data: root, Workspace: root}, bus)
	registry := session.NewRegistry(bus, writers, server.Profile, cfg.Run.MaxTurns, server.ConfigSnapshot)
	server.SetRegistry(registry)
	item, err := registry.Create("main", "coder", root)
	if err != nil {
		t.Fatal(err)
	}
	promptPath := filepath.Join(root, "system.md")
	if err := os.WriteFile(promptPath, []byte("system {{tools}} {{memory}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	renderer, err := agent.LoadTemplate(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	toolset := tools.New()
	toolset.Configure(cfg)
	runner := agent.NewRunner(bus, toolset, renderer, server.Profile, server.ConfigSnapshot)
	scheduler := agent.NewScheduler(runner, registry, bus, server.ConfigSnapshot)
	server.SetRuntime(scheduler, runner, renderer)
	return agentServerFixture{server: server, scheduler: scheduler, registry: registry, session: item, oldID: oldProfile.ID, newID: newProfile.ID}
}

func postAgentServer(t *testing.T, server *Server, agentID, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/agents/"+agentID+"/server", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func writeModelDone(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n"))
}

func waitSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal(message)
	}
}

func waitFor(t *testing.T, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !condition() {
		t.Fatal(message)
	}
}

func waitEventType(t *testing.T, stream <-chan events.Event, eventType, message string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-stream:
			if event.Type == eventType {
				return
			}
		case <-deadline:
			t.Fatal(message)
		}
	}
}
