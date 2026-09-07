package agent

import (
	"context"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/llm"
	"harness/internal/session"
	"harness/internal/tools"
)

func TestNewChatFirstRequestContainsExactlyStartupContextAndNewUser(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	cfg.Context.Accounting = "estimated"
	profile := cfg.Servers[0]
	profile.BaseURL = "http://127.0.0.1:1"
	profile.RequestTimeoutS = 1
	profile.Context.NCtx = 32768
	profile.Context.ReserveOutput = 8192
	profile.Capabilities.Tokenize = false
	profile.Capabilities.Streaming = true
	profile.Capabilities.ToolCalls = true
	profile.Capabilities.OverflowBehavior = "error"
	bus := events.NewBus()
	var request events.Event
	bus.SetSink(func(event events.Event) error {
		if event.Type == events.ModelRequest {
			request = event
		}
		return nil
	})
	toolset := tools.New(&runScriptPolicyTool{})
	runner := NewRunner(bus, toolset, &PromptRenderer{text: "system\nmemory={{memory}}\ntools={{tools}}"}, func(id string) (*config.Profile, bool) { return &profile, id == profile.ID }, func() config.Config { return cfg })
	item := &session.Session{ID: "new-chat", ServerID: profile.ID, Workspace: t.TempDir(), Runnable: true, MemoryBlock: "remembered fact", Run: session.RunState{Status: "running", MaxTurns: 40}, ToolsEnabled: map[string]bool{"run_script": true}, ToolCalls: map[string]int{}, SchemaTokens: map[string]int{}, MarginalTokens: map[string]int{}, Budget: events.Budget{NCtx: 32768, Reserve: 8192}}
	if _, err := runner.AddUser(context.Background(), item, "new request"); err != nil {
		t.Fatal(err)
	}
	_, _, _ = runner.Run(context.Background(), item, "r1")
	body, ok := request.Body.(map[string]any)
	if !ok {
		t.Fatalf("1 system prompt: request body missing: %#v", request.Body)
	}
	messages, ok := body["messages"].([]llm.Message)
	if !ok || len(messages) != 2 || messages[0].Role != "system" {
		t.Fatalf("1 system prompt: messages=%#v", messages)
	}
	if toolsBody, ok := body["tools"].([]any); !ok || len(toolsBody) != 1 {
		t.Fatalf("2 tool schemas: tools=%#v", body["tools"])
	}
	if content, ok := messages[0].Content.(string); !ok || !strings.Contains(content, "memory=remembered fact") {
		t.Fatalf("3 memory injection: content=%#v", content)
	}
	if messages[1].Role != "user" || messages[1].Content != "new request" {
		t.Fatalf("4 new user message: message=%#v", messages[1])
	}
	if len(messages) != 2 {
		t.Fatalf("5 nothing else: messages=%#v", messages)
	}
}
