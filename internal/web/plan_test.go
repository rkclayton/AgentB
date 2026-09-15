package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/agent"
	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
	"harness/internal/tools"
)

func TestPlanProposalValidationRejectsAttackerPathsAndSelfAuthorization(t *testing.T) {
	base := events.PlanProposal{ID: "p1", Kind: "reword", Path: "plan.md", OldText: "old", NewText: "new", ItemID: "2t"}
	if err := validatePlanProposal(base); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []events.PlanProposal{
		{ID: "p", Kind: "reword", Path: "../repo/main.go", OldText: "x", NewText: "y", ItemID: "2t"},
		{ID: "p", Kind: "accept", Path: "plan.md", OldText: "x", NewText: "y", ItemID: "2t"},
		{ID: "p", Kind: "reword", Path: "PLAN.md", OldText: "x", NewText: "y", ItemID: "2t"},
		{ID: "p", Kind: "agent_b_addition", Path: "AGENT_B.md", OldText: "a", NewText: "a\nb\nc", ItemID: "2t"},
	} {
		if validatePlanProposal(bad) == nil {
			t.Fatalf("accepted attacker proposal %+v", bad)
		}
	}
}

func TestPlanDogfoodFakeServerProposesThenAcceptsExactPlanDiff(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "AgentB")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "Here is the edit.\n```agentb-plan-proposals\n{\"version\":1,\"proposals\":[{\"id\":\"dogfood-add\",\"kind\":\"add\",\"path\":\"plan.md\",\"old_text\":\"# AgentB\",\"new_text\":\"# AgentB\\n[ ] 2t fake-server dogfood\",\"item_id\":\"2t\"}]}\n```"
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": content}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 10}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
	}))
	defer model.Close()
	data := t.TempDir()
	logs := filepath.Join(data, "logs")
	cfg := config.Defaults(repo)
	cfg.Context.Accounting = "estimated"
	cfg.Run.MaxConcurrent = 1
	p := runnableTestProfile("fake")
	p.BaseURL = model.URL
	cfg.Servers = []config.Profile{p}
	cfg.Agents = []config.Agent{{Name: "Dogfood", B: "fake", D: "fake", Toolset: config.FullToolset()}}
	bus := events.NewBus()
	writers, err := events.NewWriters(logs)
	if err != nil {
		t.Fatal(err)
	}
	defer writers.Close()
	bus.SetSink(writers.Write)
	server := New(&cfg, filepath.Join(data, "harness.json"), repo, RuntimeRoots{Application: repo, Data: data, Workspace: repo}, bus)
	registry := session.NewRegistry(bus, writers, server.Profile, cfg.Run.MaxTurns, server.ConfigSnapshot)
	server.SetRegistry(registry)
	plan, _, err := registry.EnsurePlan(repo)
	if err != nil {
		t.Fatal(err)
	}
	item, err := registry.CreateRole("design", "dogfood", "", "d", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	coordinator := tools.NewFileCoordinator(session.NewWorkspaceRegistry(), registry.Label, bus)
	toolset := tools.New(tools.NewEditFile(coordinator))
	toolset.Configure(cfg)
	promptPath := filepath.Join(data, "system.md")
	if err := os.WriteFile(promptPath, []byte("system"), 0o600); err != nil {
		t.Fatal(err)
	}
	renderer, err := agent.LoadTemplate(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	runner := agent.NewRunner(bus, toolset, renderer, server.Profile, server.ConfigSnapshot)
	scheduler := agent.NewScheduler(runner, registry, bus, server.ConfigSnapshot)
	server.SetRuntime(scheduler, runner, renderer)
	if _, err := scheduler.Submit(context.Background(), item.ID, "Propose one plan edit."); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for scheduler.Active(item.ID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if scheduler.Active(item.ID) {
		t.Fatal("fake planner did not finish")
	}
	messages := item.MessagesCopy()
	var proposal events.PlanProposal
	for _, message := range messages {
		if len(message.PlanProposals) > 0 {
			proposal = message.PlanProposals[0]
		}
	}
	if proposal.ID != "dogfood-add" {
		t.Fatalf("proposal=%+v messages=%+v", proposal, messages)
	}
	before, err := os.ReadFile(filepath.Join(item.PlanDir, "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(before), "dogfood") {
		t.Fatal("proposal wrote before acceptance")
	}
	item.SetPlanPage(true)
	outcome := runner.AcceptPlanEdit(context.Background(), item, proposal.Path, proposal.OldText, proposal.NewText)
	if !outcome.OK {
		t.Fatal(outcome.Content)
	}
	after, err := os.ReadFile(filepath.Join(item.PlanDir, "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != "# AgentB\n" || string(after) != "# AgentB\n[ ] 2t fake-server dogfood\n" {
		t.Fatalf("before=%q after=%q", before, after)
	}

	fallback, err := registry.CreateRole("fallback", "dogfood", repo, "b", "")
	if err != nil {
		t.Fatal(err)
	}
	fallback.SetPlanPage(true)
	fallbackProposal := events.PlanProposal{ID: "fallback-add", Kind: "add", Path: "plan.md", OldText: string(after), NewText: string(after) + "[ ] 2t fallback\n", ItemID: "2t"}
	fallback.Append(events.Message{ID: "fallback-answer", Role: "assistant", PlanProposals: []events.PlanProposal{fallbackProposal}})
	if !recordedPlanProposal(fallback, fallbackProposal) {
		t.Fatal("recorded proposal was not found")
	}
	tampered := fallbackProposal
	tampered.NewText += "tampered\n"
	if recordedPlanProposal(fallback, tampered) {
		t.Fatal("tampered proposal matched the tray record")
	}
	request := httptest.NewRequest(http.MethodPost, "/api/plan/accept", nil)
	if _, err := server.acceptPlanProposal(request, fallback, fallbackProposal); err != nil {
		t.Fatalf("fallback accept through d writer: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(item.PlanDir, "plan.md")); string(got) != fallbackProposal.NewText {
		t.Fatalf("fallback plan=%q", got)
	}
}

func TestPlanProposalValidationEnforcesKindAndItemSemantics(t *testing.T) {
	for _, bad := range []events.PlanProposal{
		{ID: "p", Kind: "add", Path: "plan.md", OldText: "old", NewText: "replacement", ItemID: "2t"},
		{ID: "p", Kind: "drop", Path: "plan.md", OldText: "old", NewText: "still here", ItemID: "2t"},
		{ID: "p", Kind: "reorder", Path: "plan.md", OldText: "one\ntwo", NewText: "one\nthree", ItemID: "2t"},
		{ID: "p", Kind: "reword", Path: "plan/items/2u.md", OldText: "old", NewText: "new", ItemID: "2t"},
	} {
		if validatePlanProposal(bad) == nil {
			t.Fatalf("accepted mismatched proposal %+v", bad)
		}
	}
	for _, good := range []events.PlanProposal{
		{ID: "p", Kind: "add", Path: "plan.md", OldText: "old", NewText: "old\nnew", ItemID: "2t"},
		{ID: "p", Kind: "drop", Path: "plan.md", OldText: "old", NewText: "", ItemID: "2t"},
		{ID: "p", Kind: "reorder", Path: "plan.md", OldText: "one\ntwo", NewText: "two\none", ItemID: "2t"},
	} {
		if err := validatePlanProposal(good); err != nil {
			t.Fatalf("rejected valid proposal %+v: %v", good, err)
		}
	}
}
