package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/tools"
)

func operatorTestServer(t *testing.T) (*Server, *tools.Shell, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "harness.json")
	cfg := config.Defaults(root)
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	server := New(&cfg, path, root, bus)
	shell := tools.NewShell(cfg.Shell)
	server.SetShellSecurity(nil, shell)
	return server, shell, path
}

func postOperatorContext(t *testing.T, server *Server, enabled bool) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"shell":{"operator_context":false}}`
	if enabled {
		body = `{"shell":{"operator_context":true}}`
	}
	request := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(body))
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func TestOperatorContextEnableRequiresOperatorOwnedHTTPClient(t *testing.T) {
	server, shell, _ := operatorTestServer(t)
	server.operatorRequest = func(*http.Request) error { return errors.New("service identity") }

	response := postOperatorContext(t, server, true)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "local process owned by") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if server.ConfigSnapshot().Shell.OperatorContext || shell.IdentityStatus().OperatorContext {
		t.Fatal("refused request enabled operator context")
	}
}

func TestServiceIdentityCannotDisableItsOwnSplit(t *testing.T) {
	server, _, _ := operatorTestServer(t)
	server.operatorRequest = func(*http.Request) error { return errors.New("service identity") }
	request := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"shell":{"service_account":{"enabled":false}}}`))
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "security-sensitive shell settings") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestOperatorContextIsRuntimeOnlyAndAutomaticallyExpires(t *testing.T) {
	server, shell, path := operatorTestServer(t)
	server.operatorRequest = func(*http.Request) error { return nil }
	server.operatorDuration = func(int) time.Duration { return 25 * time.Millisecond }

	response := postOperatorContext(t, server, true)
	if response.Code != http.StatusOK {
		t.Fatalf("enable status=%d body=%s", response.Code, response.Body)
	}
	status := shell.IdentityStatus()
	if !status.OperatorContext || status.OperatorContextExpiresAt == "" {
		t.Fatalf("identity after enable=%+v", status)
	}
	loaded, _, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Shell.OperatorContext {
		t.Fatal("operator context persisted to config")
	}
	deadline := time.Now().Add(time.Second)
	for shell.IdentityStatus().OperatorContext && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if shell.IdentityStatus().OperatorContext || server.ConfigSnapshot().Shell.OperatorContext {
		t.Fatal("operator context did not expire")
	}
	eventsSeen := server.bus.Recent("")
	var enabled, disabled bool
	for _, event := range eventsSeen {
		if event.Type != events.OperatorContext {
			continue
		}
		data, _ := event.Data.(map[string]any)
		if data["enabled"] == true {
			enabled = true
		}
		if data["enabled"] == false && data["reason"] == "automatic timeout expired" {
			disabled = true
		}
	}
	if !enabled || !disabled {
		t.Fatalf("audit events enabled=%t disabled=%t events=%#v", enabled, disabled, eventsSeen)
	}
}

func TestOperatorContextPatchMustBeIsolatedAndTimeoutIsProtected(t *testing.T) {
	server, _, _ := operatorTestServer(t)
	server.operatorRequest = func(*http.Request) error { return nil }

	request := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"shell":{"operator_context":true,"timeout_s":10}}`))
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || server.ConfigSnapshot().Shell.OperatorContext {
		t.Fatalf("mixed patch status=%d body=%s", response.Code, response.Body)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"shell":{"operator_context_timeout_minutes":1}}`))
	authorizeMutation(request, server)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "protected configuration file") {
		t.Fatalf("timeout patch status=%d body=%s", response.Code, response.Body)
	}
}
