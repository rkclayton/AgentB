package agent

import (
	"context"
	"net"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
	"harness/internal/tools"
)

func TestPublishBudgetMarksDialFailureUnreachableAndKeepsBudgetError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	profile := config.Profile{
		ID:              "offline",
		BaseURL:         "http://" + address,
		Model:           "offline",
		RequestTimeoutS: 1,
		Context:         config.Context{NCtx: 32768, ReserveOutput: 10240},
		Capabilities:    config.Capabilities{Tokenize: true, ApplyTemplate: true, ApplyTemplateTools: true},
	}
	cfg := config.Defaults(t.TempDir())
	cfg.Context.Accounting = "exact"
	cfg.Servers = []config.Profile{profile}
	bus := newCapturedBus()
	runner := NewRunner(bus.Bus, tools.New(), &PromptRenderer{text: "system"}, func(id string) (*config.Profile, bool) {
		return &profile, id == profile.ID
	}, func() config.Config { return cfg })
	item := &session.Session{
		ID:             "main",
		ServerID:       profile.ID,
		Workspace:      t.TempDir(),
		ToolsEnabled:   map[string]bool{},
		ToolCalls:      map[string]int{},
		SchemaTokens:   map[string]int{},
		MarginalTokens: map[string]int{},
	}

	runner.PublishBudget(context.Background(), item)

	var budgetError, unreachable bool
	for _, event := range bus.Recent(item.ID) {
		data, _ := event.Data.(map[string]any)
		if event.Type == events.Error && data["where"] == "budget" {
			budgetError = true
		}
		if event.Type == events.ModelUnreachable && data["host"] == address {
			unreachable = true
		}
	}
	if !budgetError || !unreachable {
		t.Fatalf("budget_error=%t model_unreachable=%t events=%#v", budgetError, unreachable, bus.Recent(item.ID))
	}
}
