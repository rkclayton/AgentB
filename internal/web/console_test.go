package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/memory"
	"harness/internal/projection"
	"harness/internal/session"
)

func consoleServer(t *testing.T) (*Server, *session.Registry, *events.Writers, *memory.Manager, *config.Config, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Defaults(filepath.Join(root, "workspace"))
	cfg.Memory.Dir = filepath.Join(root, "memory")
	cfg.Servers[0] = runnableTestProfile("local")
	cfg.Agents = []config.Agent{{Name: "Coder", B: "local", Toolset: config.FullToolset()}}
	bus := events.NewBus()
	writers, err := events.NewWriters(filepath.Join(root, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	bus.SetSink(writers.Write)
	server := New(&cfg, filepath.Join(root, "harness.json"), root, RuntimeRoots{Application: root, Data: root, Workspace: cfg.Workspace}, bus)
	registry := session.NewRegistry(bus, writers, server.Profile, 40, server.ConfigSnapshot)
	memories := memory.New(root, server.ConfigSnapshot, func(context.Context, string, string) (int, error) { return 0, nil })
	registry.SetMemoryLoader(memories.Load)
	registry.SetAgentMemoryLoader(memories.LoadAgent)
	server.SetRegistry(registry)
	server.SetWorkspaceState(nil, memories)
	server.SetProjection(projection.NewStore(), writers)
	server.operatorRequest = func(*http.Request) error { return nil }
	return server, registry, writers, memories, &cfg, root
}

func TestClosedChatDeleteRemovesOnlySessionLogsRegistryCacheAndOptedMemory(t *testing.T) {
	server, registry, writers, memories, cfg, root := consoleServer(t)
	defer writers.Close()
	workspaceFile := filepath.Join(cfg.Workspace, "kept.txt")
	exchangeFile := filepath.Join(root, "exchange", "kept.txt")
	evidenceFile := filepath.Join(root, "logs", "evidence", "kept.jsonl")
	for _, path := range []string{workspaceFile, exchangeFile, evidenceFile} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	item, err := registry.Create("delete me", "coder", cfg.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	path, _, err := memories.NoteAgent("coder", "drop poisoned preference")
	if err != nil {
		t.Fatal(err)
	}
	server.bus.Publish(events.New(events.MemoryNoted, item.ID, "", map[string]any{"path": path, "note": "drop poisoned preference", "target": "agent", "agent_id": "coder"}))
	if _, _, err := writers.RotateSession(item.ID); err != nil {
		t.Fatal(err)
	}
	if err := registry.Close(item.ID); err != nil {
		t.Fatal(err)
	}
	projected, err := server.projector.Snapshot(writers.SessionCursors())
	if err != nil || projected[item.ID].ID != item.ID {
		t.Fatalf("projector seed=%+v err=%v", projected, err)
	}
	preview := postConsole(t, server, "/api/sessions/main/delete", `{"confirm":false}`)
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), `"events":3`) || !strings.Contains(preview.Body.String(), `"jsonl_files":2`) || !strings.Contains(preview.Body.String(), "drop poisoned preference") {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body)
	}
	result := postConsole(t, server, "/api/sessions/main/delete", `{"confirm":true,"drop_memory":true}`)
	if result.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", result.Code, result.Body)
	}
	if _, ok := registry.Get(item.ID); ok {
		t.Fatal("registry entry survived")
	}
	if matches, _ := filepath.Glob(filepath.Join(root, "logs", "main-*.jsonl")); len(matches) != 0 {
		t.Fatalf("session logs=%v", matches)
	}
	if projected, err := server.projector.Snapshot(writers.SessionCursors()); err != nil || len(projected) != 0 {
		t.Fatalf("projector residue=%+v err=%v", projected, err)
	}
	if value, _ := memories.ReadAgent("coder"); value != "" {
		t.Fatalf("agent memory survived=%q", value)
	}
	for _, path := range []string{workspaceFile, exchangeFile, evidenceFile} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("kept path %s: %v", path, err)
		}
	}
}

func TestFlushMemoryNamesAndClearsAgentAndWorkspaceLayers(t *testing.T) {
	server, registry, writers, memories, cfg, _ := consoleServer(t)
	defer writers.Close()
	item, err := registry.Create("main", "coder", cfg.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := memories.Note(cfg.Workspace, "workspace fact"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := memories.NoteAgent("coder", "operator preference"); err != nil {
		t.Fatal(err)
	}
	preview := postConsole(t, server, "/api/agents/coder/memory/flush", `{"workspace":"`+strings.ReplaceAll(cfg.Workspace, `\`, `\\`)+`","confirm":false}`)
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), `"agent_entries":1`) || !strings.Contains(preview.Body.String(), `"workspace_entries":1`) {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body)
	}
	result := postConsole(t, server, "/api/agents/coder/memory/flush", `{"workspace":"`+strings.ReplaceAll(cfg.Workspace, `\`, `\\`)+`","confirm":true}`)
	if result.Code != http.StatusOK {
		t.Fatalf("flush status=%d body=%s", result.Code, result.Body)
	}
	if workspace, _ := memories.Read(cfg.Workspace); workspace != "" {
		t.Fatalf("workspace memory=%q", workspace)
	}
	if agent, _ := memories.ReadAgent("coder"); agent != "" {
		t.Fatalf("agent memory=%q", agent)
	}
	if snapshot := item.Snapshot(); snapshot.MemoryContent != "" || snapshot.AgentMemoryContent != "" {
		t.Fatalf("session memory=%+v", snapshot)
	}
}

func TestClosedChatDeleteKeepsMemoryByDefaultAndRequiresVerifiedOperator(t *testing.T) {
	server, registry, writers, memories, cfg, _ := consoleServer(t)
	defer writers.Close()
	item, err := registry.Create("delete me", "coder", cfg.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	path, _, err := memories.Note(cfg.Workspace, "keep project fact")
	if err != nil {
		t.Fatal(err)
	}
	server.bus.Publish(events.New(events.MemoryNoted, item.ID, "", map[string]any{"path": path, "note": "keep project fact", "target": "workspace", "agent_id": "coder"}))
	if err := registry.Close(item.ID); err != nil {
		t.Fatal(err)
	}
	server.operatorRequest = func(*http.Request) error { return fmt.Errorf("not operator") }
	if response := postConsole(t, server, "/api/sessions/main/delete", `{"confirm":false}`); response.Code != http.StatusForbidden {
		t.Fatalf("unverified preview status=%d body=%s", response.Code, response.Body)
	}
	server.operatorRequest = func(*http.Request) error { return nil }
	if response := postConsole(t, server, "/api/sessions/main/delete", `{"confirm":true}`); response.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body)
	}
	if value, _ := memories.Read(cfg.Workspace); !strings.Contains(value, "keep project fact") {
		t.Fatalf("memory was not kept by default: %q", value)
	}
}

func postConsole(t *testing.T, server *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}
