package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
	"harness/internal/tools"
)

func backstopFixture(t *testing.T, handler http.HandlerFunc, configure func(*config.Config)) (*Runner, *session.Session, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	cfg := config.Defaults(t.TempDir())
	cfg.Context.Accounting = "estimated"
	configure(&cfg)
	profile := cfg.Servers[0]
	profile.ID, profile.BaseURL, profile.Model = "main", server.URL, "fake"
	profile.Context.NCtx, profile.Context.ReserveOutput = 32768, 8192
	profile.Capabilities.Streaming, profile.Capabilities.ToolCalls = true, true
	profile.Capabilities.OverflowBehavior = "error"
	cfg.Servers = []config.Profile{profile}
	lookup := func(id string) (*config.Profile, bool) { return &profile, id == profile.ID }
	runner := NewRunner(events.NewBus(), tools.New(&retryWriteTool{}), &PromptRenderer{text: "system"}, lookup, func() config.Config { return cfg })
	item := &session.Session{ID: "main", ServerID: profile.ID, Workspace: t.TempDir(), Runnable: true, ToolsEnabled: map[string]bool{"write_file": true}, ToolCalls: map[string]int{}, SchemaTokens: map[string]int{}, MarginalTokens: map[string]int{}}
	item.Append(events.Message{ID: "u1", Role: "user", Category: "history", Content: "work"})
	return runner, item, server.Close
}

func TestToolBudgetStopsBeforeExcessCall(t *testing.T) {
	runner, item, closeServer := backstopFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		delta := map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "id": "c1", "type": "function", "function": map[string]any{"name": "write_file", "arguments": `{"path":"a","content":"a"}`}},
			map[string]any{"index": 1, "id": "c2", "type": "function", "function": map[string]any{"name": "write_file", "arguments": `{"path":"b","content":"b"}`}},
		}}
		writeStreamChunk(t, w, map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": "tool_calls"}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 1}})
	}, func(cfg *config.Config) { cfg.Run.MaxToolCalls = 1 })
	defer closeServer()
	reason, detail, _ := runner.Run(context.Background(), item, "r1")
	if reason != "tool_budget" || detail == "" || item.ToolCalls["write_file"] != 0 {
		t.Fatalf("reason=%q detail=%q calls=%d", reason, detail, item.ToolCalls["write_file"])
	}
}

func TestWallClockStopsBlockedModel(t *testing.T) {
	runner, item, closeServer := backstopFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		writeStreamChunk(t, w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "late"}, "finish_reason": "stop"}}})
	}, func(cfg *config.Config) { cfg.Run.MaxWallClockSeconds = 1 })
	defer closeServer()
	started := time.Now()
	reason, detail, _ := runner.Run(context.Background(), item, "r1")
	if reason != "wall_clock" || detail == "" || time.Since(started) > 3*time.Second {
		t.Fatalf("reason=%q detail=%q elapsed=%s", reason, detail, time.Since(started))
	}
}

func TestExistingTurnCeilingRemainsIndependent(t *testing.T) {
	var requests int
	runner, item, closeServer := backstopFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		delta := map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "id": "c1", "type": "function", "function": map[string]any{"name": "write_file", "arguments": `{"path":"a","content":"a"}`}},
		}}
		writeStreamChunk(t, w, map[string]any{
			"choices": []any{map[string]any{"delta": delta, "finish_reason": "tool_calls"}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 1},
		})
	}, func(cfg *config.Config) { cfg.Run.MaxTurns = 1 })
	defer closeServer()
	reason, _, turns := runner.Run(context.Background(), item, "r1")
	if reason != "turn_ceiling" || turns != 1 || requests != 1 {
		t.Fatalf("reason=%q turns=%d requests=%d", reason, turns, requests)
	}
}
