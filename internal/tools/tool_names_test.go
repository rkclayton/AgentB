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
