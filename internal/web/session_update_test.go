package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
)

func TestSessionServerReassignment(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults(root)
	cfg.Servers[0] = runnableTestProfile("first")
	second := runnableTestProfile("second")
	second.Context.ReserveOutput = 2048
	cfg.Servers = append(cfg.Servers, second, config.Profile{ID: "incomplete", Label: "Incomplete"})

	bus := events.NewBus()
	writers, err := events.NewWriters(filepath.Join(root, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writers.Close() })

	server := New(&cfg, filepath.Join(root, "harness.json"), root, RuntimeRoots{Application: root, Data: root, Workspace: cfg.Workspace}, bus)
	registry := session.NewRegistry(bus, writers, server.Profile, cfg.Run.MaxTurns, server.ConfigSnapshot)
	server.SetRegistry(registry)
	item, err := registry.Create("main", "first", root)
	if err != nil {
		t.Fatal(err)
	}
	item.Append(events.Message{ID: "m1", Role: "user", Content: "keep me"})

	response := postSessionUpdate(t, server, `{"server_id":"second"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var body struct {
		Session session.Snapshot `json:"session"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Session.ServerID != "second" || !body.Session.Runnable {
		t.Fatalf("unexpected session: %+v", body.Session)
	}
	if len(body.Session.Messages) != 1 || body.Session.Messages[0].Content != "keep me" {
		t.Fatalf("messages were not preserved: %+v", body.Session.Messages)
	}
	if body.Session.Workspace != root {
		t.Fatalf("workspace changed: %q", body.Session.Workspace)
	}
	if body.Session.Budget.NCtx != 32768 || body.Session.Budget.Reserve != 2048 {
		t.Fatalf("budget was not reset for selected profile: %+v", body.Session.Budget)
	}
	recent := bus.Recent(item.ID)
	if recent[len(recent)-1].Type != events.SessionUpdated {
		t.Fatalf("last event=%q, want %q", recent[len(recent)-1].Type, events.SessionUpdated)
	}

	item.SetRun(session.RunState{Status: "running"})
	response = postSessionUpdate(t, server, `{"server_id":"first"}`)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "session is running") {
		t.Fatalf("running status=%d body=%s", response.Code, response.Body)
	}
	if snapshot := item.Snapshot(nil); snapshot.ServerID != "second" {
		t.Fatalf("running update changed server to %q", snapshot.ServerID)
	}

	item.SetRun(session.RunState{Status: "idle"})
	response = postSessionUpdate(t, server, `{"server_id":"incomplete"}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "base_url is empty") {
		t.Fatalf("incomplete status=%d body=%s", response.Code, response.Body)
	}
}

func runnableTestProfile(id string) config.Profile {
	profile := config.Defaults(".").Servers[0]
	profile.ID, profile.Label, profile.Model = id, id, "model"
	profile.Capabilities.NCtx = 32768
	profile.Capabilities.ToolCalls = true
	profile.Capabilities.Streaming = true
	profile.Capabilities.OverflowBehavior = "error"
	return profile
}

func postSessionUpdate(t *testing.T, server *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/sessions/main", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}
