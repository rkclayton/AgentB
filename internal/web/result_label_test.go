package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
)

func TestCompletedRunResultLabelEndpoint(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults(root)
	cfg.Servers[0] = runnableTestProfile("main")
	cfg.Agents = []config.Agent{{Name: "Main", B: "main", Toolset: config.FullToolset()}}
	bus := events.NewBus()
	eventStream, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	writers, err := events.NewWriters(filepath.Join(root, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writers.Close() })
	server := New(&cfg, filepath.Join(root, "harness.json"), root, RuntimeRoots{Application: root, Data: root, Workspace: root}, bus)
	registry := session.NewRegistry(bus, writers, server.Profile, cfg.Run.MaxTurns, server.ConfigSnapshot)
	server.SetRegistry(registry)
	item, err := registry.Create("main", "main", root)
	if err != nil {
		t.Fatal(err)
	}

	item.SetRun(session.RunState{Status: "idle", LastStopReason: "done", LastRunID: "r7", ArmedDetectors: []string{"novel_action"}})
	request := httptest.NewRequest(http.MethodPost, "/api/sessions/"+item.ID+"/result-label", strings.NewReader(`{"label":"productive","run_id":"r7"}`))
	request.Header.Set("Content-Type", "application/json")
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || item.Snapshot().Run.ResultLabel != "productive" {
		t.Fatalf("status=%d body=%s run=%+v", response.Code, response.Body.String(), item.Snapshot().Run)
	}
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-eventStream:
			if event.Type == events.RunLabeled {
				data, ok := event.Data.(map[string]any)
				if event.SessionID != item.ID || event.RunID != "r7" || !ok || data["label"] != "productive" {
					t.Fatalf("event=%+v", event)
				}
				return
			}
		case <-deadline:
			t.Fatal("run.labeled event was not published")
		}
	}
}

func TestRunResultLabelEndpointRejectsActiveAndUnknownLabels(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults(root)
	cfg.Servers[0] = runnableTestProfile("main")
	cfg.Agents = []config.Agent{{Name: "Main", B: "main", Toolset: config.FullToolset()}}
	bus := events.NewBus()
	writers, err := events.NewWriters(filepath.Join(root, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writers.Close() })
	server := New(&cfg, filepath.Join(root, "harness.json"), root, RuntimeRoots{Application: root, Data: root, Workspace: root}, bus)
	registry := session.NewRegistry(bus, writers, server.Profile, cfg.Run.MaxTurns, server.ConfigSnapshot)
	server.SetRegistry(registry)
	item, err := registry.Create("main", "main", root)
	if err != nil {
		t.Fatal(err)
	}

	item.SetRun(session.RunState{Status: "running"})
	for _, test := range []struct {
		body string
		want int
	}{
		{body: `{"label":"stuck","run_id":"r7"}`, want: http.StatusConflict},
		{body: `{"label":"other"}`, want: http.StatusBadRequest},
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/sessions/"+item.ID+"/result-label", strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		authorizeMutation(request, server)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != test.want {
			t.Fatalf("body=%s status=%d response=%s", test.body, response.Code, response.Body.String())
		}
	}
	item.SetRun(session.RunState{Status: "idle", LastStopReason: "done", LastRunID: "r8"})
	request := httptest.NewRequest(http.MethodPost, "/api/sessions/"+item.ID+"/result-label", strings.NewReader(`{"label":"stuck","run_id":"r7"}`))
	request.Header.Set("Content-Type", "application/json")
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict || item.Snapshot().Run.ResultLabel != "" {
		t.Fatalf("stale status=%d body=%s run=%+v", response.Code, response.Body.String(), item.Snapshot().Run)
	}
}
