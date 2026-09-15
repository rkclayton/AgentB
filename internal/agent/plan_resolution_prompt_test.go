package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/llm"
	"harness/internal/session"
	"harness/internal/tools"
)

func TestFakeModelResolvesNamedPlanAndAsksWhenAmbiguousFromStablePrompt(t *testing.T) {
	root := t.TempDir()
	scratch, plans, repo := filepath.Join(root, "scratch"), filepath.Join(root, "plans"), filepath.Join(root, "alpha-repo")
	for _, dir := range []string{scratch, plans, repo} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	var systems []string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []llm.Message `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		systemPrompt, _ := body.Messages[0].Content.(string)
		systems = append(systems, systemPrompt)
		switch calls.Add(1) {
		case 1:
			if !strings.Contains(systemPrompt, "resolve a named plan or repo") || !strings.Contains(systemPrompt, "ask in chat when more than one plan could match") {
				t.Fatalf("resolution policy absent from system prompt: %q", systemPrompt)
			}
			arguments, _ := json.Marshal(map[string]any{"path": filepath.Join(repo, "resolved.txt"), "content": "resolved\n"})
			writeStreamChunk(t, w, map[string]any{
				"choices": []any{map[string]any{
					"delta": map[string]any{"tool_calls": []any{map[string]any{
						"index": 0, "id": "resolved-alpha", "type": "function",
						"function": map[string]any{"name": "write_file", "arguments": string(arguments)},
					}}},
					"finish_reason": "tool_calls",
				}},
				"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20},
			})
		case 2:
			writeStreamChunk(t, w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "Resolved Alpha to alpha-repo and updated resolved.txt."}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 120, "completion_tokens": 12}})
		default:
			writeStreamChunk(t, w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "Which plan or repository should I use for the shared configuration?"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 140, "completion_tokens": 12}})
		}
	}))
	defer model.Close()

	cfg := config.Defaults(scratch)
	cfg.Context.Accounting = "estimated"
	profile := cfg.Servers[0]
	profile.BaseURL, profile.Model = model.URL, "fake"
	profile.Context.NCtx, profile.Context.ReserveOutput = 32768, 8192
	profile.Capabilities.Streaming, profile.Capabilities.ToolCalls = true, true
	profile.Capabilities.OverflowBehavior = "error"
	renderer, err := LoadTemplate(filepath.Join("..", "..", "prompts", "system.md"))
	if err != nil {
		t.Fatal(err)
	}
	coordinator := tools.NewFileCoordinator(session.NewWorkspaceRegistry(), func(id string) string { return id }, events.NewBus())
	toolset := tools.New(tools.NewWriteFile(coordinator))
	lookup := func(id string) (*config.Profile, bool) { return &profile, id == profile.ID }
	item := &session.Session{ID: "resolution", ServerID: profile.ID, Role: "b", Workspace: scratch, PlansRoot: plans, PlanRepos: func() []string { return []string{repo} }, Runnable: true, Run: session.RunState{Status: "running", MaxTurns: 4}, ToolsEnabled: map[string]bool{"write_file": true}, ToolCalls: map[string]int{}, LastSeen: map[string]time.Time{}, SchemaTokens: map[string]int{}, MarginalTokens: map[string]int{}, Budget: events.Budget{NCtx: 32768, Reserve: 8192}}
	runner := NewRunner(events.NewBus(), toolset, renderer, lookup, func() config.Config { return cfg })
	for _, brief := range []string{"Fix resolved.txt in the Alpha plan.", "Fix the shared configuration."} {
		if _, err := runner.AddUser(context.Background(), item, brief); err != nil {
			t.Fatal(err)
		}
		if reason, detail, _ := runner.Run(context.Background(), item, "run"); reason != "done" || detail != "" {
			t.Fatalf("run=(%q,%q)", reason, detail)
		}
	}
	if data, err := os.ReadFile(filepath.Join(repo, "resolved.txt")); err != nil || string(data) != "resolved\n" {
		t.Fatalf("resolved plan write=%q err=%v", data, err)
	}
	for i := 1; i < len(systems); i++ {
		if systems[i] != systems[0] {
			t.Fatalf("system prompt changed between requests %d and 0", i)
		}
	}
	transcript := item.Snapshot().Messages
	joined := ""
	for _, message := range transcript {
		joined += message.Content + "\n"
	}
	if !strings.Contains(joined, "Resolved Alpha to alpha-repo") || !strings.Contains(joined, "Which plan or repository should I use") {
		t.Fatalf("resolution/ambiguity transcript=%q", joined)
	}
	t.Log("resolved transcript: Resolved Alpha to alpha-repo and updated resolved.txt.")
	t.Log("ambiguity transcript: Which plan or repository should I use for the shared configuration?")
}
