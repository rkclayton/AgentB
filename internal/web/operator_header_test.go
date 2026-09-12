package web

import (
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
)

func TestSharedShellIsServedOnAllThreePages(t *testing.T) {
	webDir := filepath.Join("..", "..", "web")
	cfg := config.Defaults(t.TempDir())
	root := t.TempDir()
	server := New(&cfg, filepath.Join(root, "harness.json"), webDir, RuntimeRoots{Application: webDir, Data: root, Workspace: cfg.Workspace}, events.NewBus())

	for _, item := range []struct{ path, page string }{
		{"/", "console"}, {"/chat", "console"}, {"/plan", "plan"},
	} {
		request := httptest.NewRequest(http.MethodGet, item.path, nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		body := response.Body.String()
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d", item.path, response.Code)
		}
		if !strings.Contains(body, `id="app-shell"`) || !strings.Contains(body, `data-page="`+item.page+`"`) {
			t.Fatalf("GET %s does not contain the %s shared shell", item.path, item.page)
		}
	}
	source, err := os.ReadFile(filepath.Join(webDir, "js", "shell.js"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "right.append(pages, settings)") || strings.Contains(text, "right.append(stop") || strings.Contains(text, "shell-operator-status") {
		t.Fatalf("shared shell right slot must contain page switch and Settings only")
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
