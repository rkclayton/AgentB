package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func TestExactSchemaAttributionReturnsTokenizerFailure(t *testing.T) {
	var tokenizeCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apply-template":
			fmt.Fprint(w, `{"prompt":"rendered"}`)
		case "/tokenize":
			if tokenizeCalls.Add(1) == 4 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			fmt.Fprint(w, `{"tokens":[1]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	profile := config.Profile{BaseURL: server.URL, RequestTimeoutS: 5, Capabilities: config.Capabilities{Tokenize: true, ApplyTemplate: true, ApplyTemplateTools: true}}
	item := &session.Session{ID: "exact", SchemaTokens: map[string]int{}}
	_, err := NewBudgeter().Measure(context.Background(), &profile, item, config.GlobalContext{}, budgetInput{SystemBase: "system", System: "system", Schemas: []any{map[string]any{"name": "active"}}, AllSchemas: map[string]any{"read_file": map[string]any{"name": "read_file"}}}, false)
	if err == nil || !strings.Contains(err.Error(), "count schema read_file") {
		t.Fatalf("schema attribution error=%v", err)
	}
}
