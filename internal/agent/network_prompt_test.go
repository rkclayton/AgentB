package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/llm"
	"harness/internal/session"
	"harness/internal/tools"
)

func TestFakeModelStatesLANBoundaryFromStableSessionPrompt(t *testing.T) {
	var systems []string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []llm.Message `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Messages) < 2 {
			t.Fatalf("messages=%+v", body.Messages)
		}
		system, _ := body.Messages[0].Content.(string)
		systems = append(systems, system)
		answer := "I cannot ping 192.168.50.10 from the service context because that LAN subnet is not enabled. I can offer Run as you for operator approval under the operator's non-elevated identity."
		writeStreamChunk(t, w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": answer}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 30}})
	}))
	defer model.Close()

	cfg := config.Defaults(t.TempDir())
	cfg.Context.Accounting = "estimated"
	profile := cfg.Servers[0]
	profile.BaseURL, profile.Model = model.URL, "fake"
	profile.Context.NCtx, profile.Context.ReserveOutput = 32768, 8192
	profile.Capabilities.Streaming, profile.Capabilities.ToolCalls = true, true
	profile.Capabilities.OverflowBehavior = "error"
	lookup := func(id string) (*config.Profile, bool) { return &profile, id == profile.ID }
	item := &session.Session{ID: "network", ServerID: profile.ID, Workspace: t.TempDir(), NetworkBoundary: session.NetworkBoundary(cfg), Runnable: true, Run: session.RunState{Status: "running", MaxTurns: 4}, ToolsEnabled: map[string]bool{}, ToolCalls: map[string]int{}, SchemaTokens: map[string]int{}, MarginalTokens: map[string]int{}, Budget: events.Budget{NCtx: 32768, Reserve: 8192}}
	runner := NewRunner(events.NewBus(), tools.New(), &PromptRenderer{text: "{{network_boundary}}"}, lookup, func() config.Config { return cfg })
	for _, request := range []string{"Ping 192.168.50.10.", "Repeat the network answer."} {
		if _, err := runner.AddUser(context.Background(), item, request); err != nil {
			t.Fatal(err)
		}
		if reason, detail, _ := runner.Run(context.Background(), item, "run"); reason != "done" || detail != "" {
			t.Fatalf("run=(%q,%q)", reason, detail)
		}
	}
	if len(systems) != 2 || systems[0] != systems[1] {
		t.Fatalf("system prompts are not byte-identical: %#v", systems)
	}
	transcript := item.Snapshot().Messages
	joined := ""
	for _, message := range transcript {
		joined += message.Content + "\n"
	}
	if !strings.Contains(joined, "cannot ping 192.168.50.10") || !strings.Contains(joined, "offer Run as you") {
		t.Fatalf("transcript=%q", joined)
	}
	if strings.Contains(strings.ToLower(joined), "this environment has no network") {
		t.Fatalf("invented environment claim in transcript=%q", joined)
	}
}
