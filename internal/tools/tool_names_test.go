package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/session"
)

type cancellationProbeTool struct{ called bool }

func (t *cancellationProbeTool) Name() string           { return "probe" }
func (t *cancellationProbeTool) Description() string    { return "test" }
func (t *cancellationProbeTool) Schema() map[string]any { return map[string]any{} }
func (t *cancellationProbeTool) Call(context.Context, *session.Session, map[string]any) (string, error) {
	t.called = true
	return "success", nil
}

func TestRegistryDoesNotDispatchCancelledCall(t *testing.T) {
	probe := &cancellationProbeTool{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outcome := New(probe).CallDetailed(ctx, &session.Session{ToolsEnabled: map[string]bool{"probe": true}}, "probe", nil)
	if outcome.OK || probe.called || !strings.Contains(outcome.Content, "context canceled") {
		t.Fatalf("cancelled outcome=%+v called=%v", outcome, probe.called)
	}
}

func TestSearchTextDoesNotHideReadFailure(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "source.txt")
	if err := os.WriteFile(path, []byte("needle"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults(workspace)
	grep := NewGrep(cfg.Tools.Grep, cfg.Tools.ListDir)
	grep.read = func(name string) ([]byte, error) { return nil, fmt.Errorf("read %s failed", filepath.Base(name)) }
	result, err := grep.Call(context.Background(), &session.Session{Workspace: workspace}, map[string]any{"pattern": "needle"})
	if err == nil || result != "" || !strings.Contains(err.Error(), "source.txt failed") {
		t.Fatalf("search result=%q err=%v", result, err)
	}
}

func TestRenamedToolsRegisterExposeSchemasAndExecute(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sample.go"), []byte("package sample\n// unique-search-marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults(workspace)
	cfg.Tools.Fetch.AllowInternalHosts = []string{"127.0.0.1"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "remote marker")
	}))
	defer server.Close()

	registry := New(NewGrep(cfg.Tools.Grep, cfg.Tools.ListDir), NewGlob(), NewFetch(cfg.Tools.Fetch))
	enabled := map[string]bool{"search_text": true, "find_files": true, "fetch_url": true}
	if got, want := registry.Names(enabled), []string{"search_text", "find_files", "fetch_url"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("registered names = %v, want %v", got, want)
	}
	for _, raw := range registry.Schemas(enabled) {
		schema := raw.(map[string]any)["function"].(map[string]any)
		name := schema["name"].(string)
		if !enabled[name] {
			t.Fatalf("unexpected schema name %q", name)
		}
	}

	item := &session.Session{Workspace: workspace, ToolsEnabled: enabled, LastSeen: map[string]time.Time{}}
	checks := []struct {
		name string
		args map[string]any
		want string
	}{
		{name: "search_text", args: map[string]any{"pattern": "unique-search-marker", "path": "."}, want: "sample.go:2"},
		{name: "find_files", args: map[string]any{"pattern": "*.go", "path": "."}, want: "sample.go"},
		{name: "fetch_url", args: map[string]any{"url": server.URL}, want: "> remote marker"},
	}
	for _, check := range checks {
		outcome := registry.CallDetailed(context.Background(), item, check.name, check.args)
		if !outcome.OK || !strings.Contains(outcome.Content, check.want) {
			t.Errorf("%s outcome = %#v, want content %q", check.name, outcome, check.want)
		}
	}
}
