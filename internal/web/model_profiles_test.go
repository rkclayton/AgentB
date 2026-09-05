package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/credential"
	"harness/internal/events"
	"harness/internal/session"
)

func TestConfigPOSTAssignsEitherProfileToEitherRole(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "harness.json")
	cfg := config.Defaults(root)
	cfg.Servers[0] = runnableTestProfile("local")
	small := cfg.Servers[0]
	small.ID, small.Label = "small", "Small"
	cfg.Servers = append(cfg.Servers, small)
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	server := New(&cfg, path, root, RuntimeRoots{Application: root, Data: root, Workspace: cfg.Workspace}, events.NewBus())
	writers, err := events.NewWriters(filepath.Join(root, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writers.Close() })
	server.SetRegistry(session.NewRegistry(events.NewBus(), writers, server.Profile, cfg.Run.MaxTurns, server.ConfigSnapshot))

	response := postConfigPatch(t, server, `{"roles":{"main":"small","aux":"local"}}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var returned config.Config
	if err := json.Unmarshal(response.Body.Bytes(), &returned); err != nil {
		t.Fatal(err)
	}
	if returned.Roles.Main != "small" || returned.Roles.Aux != "local" {
		t.Fatalf("roles=%+v", returned.Roles)
	}

	response = postConfigPatch(t, server, `{"roles":{"main":"small","aux":"small"}}`)
	if response.Code != http.StatusOK {
		t.Fatalf("same-profile status=%d body=%s", response.Code, response.Body)
	}
	if got, _ := server.ConfigSnapshot().RoleProfile("aux"); got.ID != "small" {
		t.Fatalf("aux resolved to %q", got.ID)
	}
	request := httptest.NewRequest(http.MethodDelete, "/api/servers/small", nil)
	authorizeMutation(request, server)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "assigned to a model role") {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(`{"label":"uses main"}`))
	request.Header.Set("Content-Type", "application/json")
	authorizeMutation(request, server)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !bytes.Contains(response.Body.Bytes(), []byte(`"server_id":"small"`)) {
		t.Fatalf("new session status=%d body=%s", response.Code, response.Body)
	}
}

func TestConfigPOSTStoresProfileSecretOutsideJSON(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DPAPI is Windows-only")
	}
	root := t.TempDir()
	path := filepath.Join(root, "harness.json")
	cfg := config.Defaults(root)
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	server := New(&cfg, path, root, RuntimeRoots{Application: root, Data: root, Workspace: cfg.Workspace}, bus)
	const secret = "profile-test-secret"

	response := postConfigPatch(t, server, `{"servers":[{"id":"local","credential":"homepc","api_key":"`+secret+`"}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if bytes.Contains(response.Body.Bytes(), []byte(secret)) || !bytes.Contains(response.Body.Bytes(), []byte(`"api_key":"•••• set"`)) {
		t.Fatalf("unsafe response: %s", response.Body)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(persisted, []byte(secret)) || bytes.Contains(persisted, []byte("api_key")) || !bytes.Contains(persisted, []byte(`"credential": "homepc"`)) {
		t.Fatalf("unsafe config: %s", persisted)
	}
	store, err := credential.NewNamed(root, "homepc")
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Read()
	if err != nil || string(value) != secret {
		t.Fatalf("stored value=%q err=%v", value, err)
	}
	eventJSON, err := json.Marshal(bus.Recent(""))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(eventJSON, []byte(secret)) {
		t.Fatalf("secret entered event stream: %s", eventJSON)
	}
}

func postConfigPatch(t *testing.T, server *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}
