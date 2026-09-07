package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
)

func TestAutoRenameUsesAuxAtTwentyTurns(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "  Fix Shared Shell!!!  "}, "finish_reason": "stop"}},
		})
	}))
	defer server.Close()
	aux := config.Profile{ID: "aux", BaseURL: server.URL, Model: "aux-model", RequestTimeoutS: 2}
	cfg := config.Defaults(t.TempDir())
	cfg.Servers = append(cfg.Servers, aux)
	cfg.Roles.Aux = aux.ID
	item := &session.Session{ID: "s2", Messages: []events.Message{{Role: "user", Content: "make Chat and Console use one shell"}}}
	for range autoRenameEveryTurns {
		item.RecordModelTurn()
	}
	var id, name, by string
	runner := &Runner{cfg: func() config.Config { return cfg }, renameSession: func(gotID, gotName, gotBy string) error {
		id, name, by = gotID, gotName, gotBy
		return nil
	}}
	runner.maybeAutoRename(context.Background(), item)
	if id != "s2" || name != "Fix Shared Shell" || by != "aux" {
		t.Fatalf("rename=(%q,%q,%q)", id, name, by)
	}
	messages := body["messages"].([]any)
	if len(messages) != 2 || body["tools"] != nil {
		t.Fatalf("aux request=%#v", body)
	}
}

func TestAutoRenameHonorsUserPinAndToggle(t *testing.T) {
	item := &session.Session{ID: "s2", NamePinned: true, Messages: []events.Message{{Role: "user", Content: "hello"}}}
	for range autoRenameEveryTurns {
		item.RecordModelTurn()
	}
	called := false
	cfg := config.Defaults(t.TempDir())
	cfg.Roles.Aux = "local"
	runner := &Runner{cfg: func() config.Config { return cfg }, renameSession: func(string, string, string) error { called = true; return nil }}
	runner.maybeAutoRename(context.Background(), item)
	if called {
		t.Fatal("pinned chat was auto-renamed")
	}
	item.NamePinned = false
	cfg.Chat.AutoRename = false
	runner.maybeAutoRename(context.Background(), item)
	if called {
		t.Fatal("disabled auto-rename ran")
	}
}

func TestCleanChatNameLimitsWordsAndPunctuation(t *testing.T) {
	if got := cleanChatName("'one two three four five six seven.'"); got != "one two three four five six" {
		t.Fatalf("clean name=%q", got)
	}
}
