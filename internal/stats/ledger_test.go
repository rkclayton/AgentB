package stats

import (
	"path/filepath"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
)

func TestLedgerAggregatesAtRunEndAndSurvivesSessionDeletion(t *testing.T) {
	root := t.TempDir()
	bus := events.NewBus()
	writers, err := events.NewWriters(filepath.Join(root, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	defer writers.Close()
	cfg := config.Defaults(root)
	profile := cfg.Servers[0]
	profile.BaseURL, profile.Model, profile.Context.NCtx = "http://127.0.0.1:1", "model", 32768
	profile.Capabilities.Streaming, profile.Capabilities.ToolCalls, profile.Capabilities.OverflowBehavior = true, true, "error"
	cfg.Servers[0] = profile
	registry := session.NewRegistry(bus, writers, func(string) (*config.Profile, bool) { return &profile, true }, 40, func() config.Config { return cfg })
	manager := New(root, registry, bus)
	item, err := registry.Create("main", cfg.DefaultAgentID(), root)
	if err != nil {
		t.Fatal(err)
	}
	runID := "r1"
	bus.Publish(events.New(events.RunStarted, item.ID, runID, map[string]any{"run_id": runID}))
	bus.Publish(events.New(events.ModelRequest, item.ID, runID, map[string]any{"turn": 1}))
	bus.Publish(events.New(events.ModelResponse, item.ID, runID, map[string]any{"content": "done", "duration_ms": 12, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "cached_tokens": 40}}))
	bus.Publish(events.New(events.ToolResult, item.ID, runID, map[string]any{"name": "read_file", "ok": false}))
	bus.Publish(events.New(events.ApprovalRequired, item.ID, runID, map[string]any{"call_id": "c1"}))
	bus.Publish(events.New(events.RunStopped, item.ID, runID, map[string]any{"reason": "done"}))
	deadline := time.Now().Add(time.Second)
	var ledger Ledger
	for time.Now().Before(deadline) {
		ledger = manager.Snapshot(cfg.DefaultAgentID())
		if ledger.Agent.Runs == 1 && ledger.Agent.PromptTokens == 100 && ledger.Agent.Reliability.Completed == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if ledger.Version != Version || ledger.Agent.Chats != 1 || ledger.Agent.Runs != 1 || ledger.Agent.Turns != 1 || ledger.Agent.Tools["read_file"].Failures != 1 || ledger.Agent.Reliability.Completed != 1 || ledger.Agent.Reliability.Interventions != 1 {
		t.Fatalf("ledger=%+v", ledger)
	}
	if ledger.Agent.PromptTokens != 100 || ledger.Agent.CompletionTokens != 20 || ledger.Agent.CachedTokens != 40 || len(ledger.Profiles["local"].ModelResponseMS) != 1 {
		t.Fatalf("tokens/profile=%+v", ledger)
	}
	before := manager.Snapshot(cfg.DefaultAgentID())
	if err := registry.Close(item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Delete(item.ID); err != nil {
		t.Fatal(err)
	}
	after := manager.Snapshot(cfg.DefaultAgentID())
	if before.Agent.Runs != after.Agent.Runs || before.Agent.PromptTokens != after.Agent.PromptTokens {
		t.Fatalf("delete altered ledger before=%+v after=%+v", before, after)
	}
}

func TestReliabilityAttributionCountsKnownFailuresWithoutCallingStopsFailures(t *testing.T) {
	counters := Counters{Tools: map[string]Tool{}}
	add(&counters, events.New(events.ApprovalDecided, "main", "r1", map[string]any{"decision": "session"}), nil)
	for _, reason := range []string{"model_error", "length", "tool_errors", "profile_not_runnable", "context_ceiling", "turn_ceiling", "safe", "emergency", "user_stop"} {
		add(&counters, events.New(events.RunStopped, "main", "r1", map[string]any{"reason": reason}), nil)
	}
	if counters.ApprovalsApproved != 1 {
		t.Fatalf("approved=%d", counters.ApprovalsApproved)
	}
	got := counters.Reliability
	if got.ModelFailures != 2 || got.HarnessFailures != 3 || got.BriefFailures != 1 {
		t.Fatalf("attribution=%+v", got)
	}
}
