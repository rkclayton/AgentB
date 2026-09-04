package agent

import (
	"context"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/llm"
	"harness/internal/session"
)

func TestBudgetAccountsFetchedResultsSeparately(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	profile := cfg.Servers[0]
	item := &session.Session{ID: "fetch-budget", SchemaTokens: map[string]int{}}
	message := llm.Message{Role: "tool", Content: "untrusted fetched text"}
	record := events.Message{ID: "m-fetch", Role: "tool", Content: "untrusted fetched text", Category: "fetched"}
	budget, err := NewBudgeter().Measure(context.Background(), &profile, item, config.GlobalContext{Accounting: "estimated"}, budgetInput{SystemBase: "system", System: "system", Messages: []llm.Message{message}, Records: []events.Message{record}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if budget.Categories["fetched"] == 0 {
		t.Fatalf("fetched category was not charged: %+v", budget.Categories)
	}
	if budget.Categories["results"] != 0 {
		t.Fatalf("fetch leaked into results: %+v", budget.Categories)
	}
}
