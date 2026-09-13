package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/memory"
	"harness/internal/session"
	workspaceinfo "harness/internal/workspace"
)

func TestBoundDirectorySessionAndNativeFolderPickerRoutes(t *testing.T) {
	root, data, logs := t.TempDir(), t.TempDir(), t.TempDir()
	bound := filepath.Join(root, "repo")
	if err := os.MkdirAll(bound, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bound, "AGENTS.md"), []byte("project route rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults(filepath.Join(root, "default"))
	cfg.Servers = []config.Profile{{ID: "main", Label: "Main", BaseURL: "http://127.0.0.1:8000", Model: "model", Context: config.Context{NCtx: 32768, ReserveOutput: 8192}, Capabilities: config.Capabilities{Streaming: true, ToolCalls: true, OverflowBehavior: "error"}}}
	cfg.Agents = []config.Agent{{Name: "Main", B: "main", D: "main", Toolset: config.FullToolset()}}
	bus := events.NewBus()
	writers, err := events.NewWriters(logs)
	if err != nil {
		t.Fatal(err)
	}
	defer writers.Close()
	server := New(&cfg, filepath.Join(data, "harness.json"), root, RuntimeRoots{Data: data, Workspace: cfg.Workspace}, bus)
	memories := memory.New(data, server.ConfigSnapshot, func(context.Context, string, string) (int, error) { return 0, nil })
	workspaces := workspaceinfo.New(data, memories.Path)
	registry := session.NewRegistry(bus, writers, server.Profile, 40, server.ConfigSnapshot)
	registry.SetMemoryLoader(memories.Load)
	registry.SetWorkspaceManager(workspaces)
	server.SetRegistry(registry)
	server.SetWorkspaceState(workspaces, memories)
	server.operatorRequest = func(*http.Request) error { return nil }
	server.pickFolder = func(initial string) (string, error) {
		if initial != cfg.Workspace {
			t.Fatalf("initial=%q", initial)
		}
		return bound, nil
	}

	call := func(method, path string, body any) *httptest.ResponseRecorder {
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		request := httptest.NewRequest(method, path, bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-AgentB-Mutation-Token", server.mutationToken)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	picked := call(http.MethodPost, "/api/pick-folder", map[string]any{})
	if picked.Code != 200 {
		t.Fatalf("picker %d %s", picked.Code, picked.Body.String())
	}
	created := call(http.MethodPost, "/api/sessions", map[string]any{"server_id": "main", "workspace": bound})
	if created.Code != 201 {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	var response struct {
		Session session.Snapshot `json:"session"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Session.WorkspaceDir != filepath.Clean(bound) || response.Session.WorkspaceMissing || len(response.Session.ProjectFiles) != 1 {
		t.Fatalf("session=%+v", response.Session)
	}
	planDir := filepath.Join(data, "plans", "stable")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "plan.md"), []byte("# Stable display name\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dCreated := call(http.MethodPost, "/api/sessions", map[string]any{"agent_id": "main", "workspace": bound, "role": "d", "plan_id": "stable"})
	if dCreated.Code != http.StatusCreated {
		t.Fatalf("d create %d %s", dCreated.Code, dCreated.Body.String())
	}
	var dResponse struct {
		Session session.Snapshot `json:"session"`
	}
	if err := json.Unmarshal(dCreated.Body.Bytes(), &dResponse); err != nil {
		t.Fatal(err)
	}
	if dResponse.Session.Role != "d" || dResponse.Session.PlanID != "stable" || dResponse.Session.PlanName != "Stable display name" || dResponse.Session.ServerID != "main" {
		t.Fatalf("d session=%+v", dResponse.Session)
	}
	plans := call(http.MethodGet, "/api/plans", nil)
	if plans.Code != http.StatusOK || !bytes.Contains(plans.Body.Bytes(), []byte(`"id":"stable","name":"Stable display name"`)) {
		t.Fatalf("plans %d %s", plans.Code, plans.Body.String())
	}
	listed := call(http.MethodGet, "/api/workspaces", nil)
	var known []workspaceinfo.Entry
	if err := json.Unmarshal(listed.Body.Bytes(), &known); err != nil {
		t.Fatal(err)
	}
	if listed.Code != 200 || len(known) != 1 || known[0].Dir != filepath.Clean(bound) {
		t.Fatalf("workspaces %d %s", listed.Code, listed.Body.String())
	}
}

func TestNamedOutsideDirectoryFindsExistingPathWithSpacesOnlyOutsideBinding(t *testing.T) {
	root := t.TempDir()
	bound := filepath.Join(root, "bound")
	outside := filepath.Join(t.TempDir(), "outside repo")
	inside := filepath.Join(bound, "inside repo")
	for _, dir := range []string{bound, outside, inside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := namedOutsideDirectory(`please inspect "`+outside+`" and report`, bound); got != filepath.Clean(outside) {
		t.Fatalf("outside=%q want=%q", got, outside)
	}
	if got := namedOutsideDirectory("please inspect "+inside+" and report", bound); got != "" {
		t.Fatalf("inside path offered=%q", got)
	}
	if got := namedOutsideDirectory(`C:\definitely-not-an-agentb-directory`, bound); got != "" {
		t.Fatalf("missing path offered=%q", got)
	}
}
