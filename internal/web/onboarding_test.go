package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
)

func TestFirstRunRoutesToSetupAndSkipRemainsAvailable(t *testing.T) {
	webDir := t.TempDir()
	for name, body := range map[string]string{"index.html": "workspace", "setup.html": "setup"} {
		if err := os.WriteFile(filepath.Join(webDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Defaults(t.TempDir())
	cfg.Servers = []config.Profile{}
	cfg.Agents = []config.Agent{}
	server := New(&cfg, filepath.Join(t.TempDir(), "harness.json"), webDir, RuntimeRoots{Application: t.TempDir()}, events.NewBus())

	redirect := httptest.NewRecorder()
	server.Handler().ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, "/chat", nil))
	if redirect.Code != http.StatusTemporaryRedirect || redirect.Header().Get("Location") != "/setup" {
		t.Fatalf("first-run response=%d location=%q", redirect.Code, redirect.Header().Get("Location"))
	}
	setup := httptest.NewRecorder()
	server.Handler().ServeHTTP(setup, httptest.NewRequest(http.MethodGet, "/setup", nil))
	if setup.Code != http.StatusOK || strings.TrimSpace(setup.Body.String()) != "setup" {
		t.Fatalf("setup response=%d %q", setup.Code, setup.Body.String())
	}
	skipped := httptest.NewRecorder()
	server.Handler().ServeHTTP(skipped, httptest.NewRequest(http.MethodGet, "/chat?setup=skip", nil))
	if skipped.Code != http.StatusOK || strings.TrimSpace(skipped.Body.String()) != "workspace" {
		t.Fatalf("skip response=%d %q", skipped.Code, skipped.Body.String())
	}
}

func TestLocalDetectionEndpointReturnsDetectorReport(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	server := New(&cfg, filepath.Join(t.TempDir(), "harness.json"), t.TempDir(), RuntimeRoots{Application: t.TempDir()}, events.NewBus())
	server.detectLocal = func(context.Context, string) (any, error) {
		return map[string]any{"supported": true, "recommendation_memory_bytes": 32 << 30}, nil
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/local-detection", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"supported":true`) {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}
