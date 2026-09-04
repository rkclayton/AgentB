package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
)

func authorizeMutation(request *http.Request, server *Server) {
	request.Header.Set("X-AgentB-Mutation-Token", server.mutationToken)
}

func TestConfigPOSTRetainsSchemaStamp(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "harness.json")
	cfg := config.Defaults(root)
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	server := New(&cfg, path, root, events.NewBus())
	request := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"approval":{"mode":"mutating"}}`))
	authorizeMutation(request, server)
	request.Host = "example.com"
	request.Header.Set("Origin", "http://example.com")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var returned config.Config
	if err := json.Unmarshal(response.Body.Bytes(), &returned); err != nil {
		t.Fatal(err)
	}
	if returned.ConfigVersion != config.CurrentConfigVersion || returned.Approval.Mode != config.ApprovalModeMutating {
		t.Fatalf("response version=%d mode=%q", returned.ConfigVersion, returned.Approval.Mode)
	}
	loaded, _, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ConfigVersion != config.CurrentConfigVersion || loaded.Approval.Mode != config.ApprovalModeMutating {
		t.Fatalf("disk version=%d mode=%q", loaded.ConfigVersion, loaded.Approval.Mode)
	}
}

func TestMutationGuardRequiresLaunchTokenAndSameOrigin(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults(root)
	server := New(&cfg, filepath.Join(root, "harness.json"), root, events.NewBus())

	request := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"approval":{"mode":"all"}}`))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "mutation token") {
		t.Fatalf("missing token status=%d body=%s", response.Code, response.Body)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"approval":{"mode":"all"}}`))
	authorizeMutation(request, server)
	request.Header.Set("Origin", "https://example.invalid")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "cross-origin") {
		t.Fatalf("cross-origin status=%d body=%s", response.Code, response.Body)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"approval":{"mode":"all"}}`))
	authorizeMutation(request, server)
	request.Header.Set("Origin", "http://example.com")
	request.Host = "example.com"
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("same-origin status=%d body=%s", response.Code, response.Body)
	}
}

func TestSecurityHeaders(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults(root)
	server := New(&cfg, filepath.Join(root, "harness.json"), root, events.NewBus())
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	for _, name := range []string{"Content-Security-Policy", "Referrer-Policy", "X-Content-Type-Options", "X-Frame-Options"} {
		if response.Header().Get(name) == "" {
			t.Errorf("missing %s", name)
		}
	}
}

func TestSnapshotToolInventoryUsesPublicNames(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults(root)
	server := New(&cfg, filepath.Join(root, "harness.json"), root, events.NewBus())
	raw := server.snapshotWithSessions(map[string]any{}, false)["tools"].([]map[string]string)
	names := make([]string, 0, len(raw))
	for _, item := range raw {
		names = append(names, item["name"])
		if item["description"] == "" {
			t.Errorf("tool %q has no description", item["name"])
		}
	}
	want := []string{"read_file", "list_dir", "write_file", "edit_file", "search_text", "shell", "remember", "recall", "fetch_url", "find_files"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("tool inventory = %v, want %v", names, want)
	}
}
