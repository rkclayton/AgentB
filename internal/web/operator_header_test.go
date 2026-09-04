package web

import (
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
)

func TestOperatorControlIsServedInBothHeaders(t *testing.T) {
	webDir := filepath.Join("..", "..", "web")
	cfg := config.Defaults(t.TempDir())
	root := t.TempDir()
	server := New(&cfg, filepath.Join(root, "harness.json"), webDir, RuntimeRoots{Application: webDir, Data: root, Workspace: cfg.Workspace}, events.NewBus())

	for _, item := range []struct {
		path, stopID, operatorID string
	}{
		{"/", `id="stop"`, `id="operator-status"`},
		{"/chat", `id="chat-stop"`, `id="chat-operator-status"`},
	} {
		request := httptest.NewRequest(http.MethodGet, item.path, nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		body := response.Body.String()
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d", item.path, response.Code)
		}
		stopAt := strings.Index(body, item.stopID)
		operatorAt := strings.Index(body, item.operatorID)
		if stopAt < 0 || operatorAt < stopAt {
			t.Fatalf("GET %s does not place %s after %s", item.path, item.operatorID, item.stopID)
		}
		buttonEnd := strings.Index(body[operatorAt:], "</button>")
		if buttonEnd < 0 {
			t.Fatalf("GET %s operator button is incomplete", item.path)
		}
		button := body[operatorAt : operatorAt+buttonEnd]
		if strings.Contains(button, " disabled") || !strings.Contains(button, `operator-off-24.png`) {
			t.Fatalf("GET %s operator button=%q", item.path, button)
		}
	}
}

func TestOperatorHeaderAssetsAreServedAtDeclaredDimensions(t *testing.T) {
	webDir := filepath.Join("..", "..", "web")
	cfg := config.Defaults(t.TempDir())
	root := t.TempDir()
	server := New(&cfg, filepath.Join(root, "harness.json"), webDir, RuntimeRoots{Application: webDir, Data: root, Workspace: cfg.Workspace}, events.NewBus())

	for _, item := range []struct {
		path string
		size int
	}{
		{"/static/assets/operator-off-24.png", 24},
		{"/static/assets/operator-off-48.png", 48},
		{"/static/assets/operator-on-24.png", 24},
		{"/static/assets/operator-on-48.png", 48},
	} {
		request := httptest.NewRequest(http.MethodGet, item.path, nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("GET %s status=%d content-type=%q", item.path, response.Code, response.Header().Get("Content-Type"))
		}
		decoded, err := png.DecodeConfig(response.Body)
		if err != nil {
			t.Fatalf("decode %s: %v", item.path, err)
		}
		if decoded.Width != item.size || decoded.Height != item.size {
			t.Fatalf("%s dimensions=%dx%d, want %dx%d", item.path, decoded.Width, decoded.Height, item.size, item.size)
		}
	}
}
