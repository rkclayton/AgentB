package agent

import (
	"context"
	"encoding/json"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apply-template":
			var body struct {
				Tools []map[string]any `json:"tools"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if len(body.Tools) == 1 && schemaName(body.Tools[0]) == "read_file" {
				fmt.Fprint(w, `{"prompt":"fail-schema"}`)
				return
			}
			fmt.Fprint(w, `{"prompt":"rendered"}`)
		case "/tokenize":
			var body struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if body.Content == "fail-schema" {
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
	item := &session.Session{ID: "exact", SchemaTokens: map[string]int{}, MarginalTokens: map[string]int{}}
	readFile := testSchema("read_file")
	_, err := NewBudgeter().Measure(context.Background(), &profile, item, config.GlobalContext{}, budgetInput{SystemBase: "system", System: "system", Schemas: []any{testSchema("active")}, AllSchemas: map[string]any{"read_file": readFile}}, false)
	if err == nil || !strings.Contains(err.Error(), "count schema read_file") {
		t.Fatalf("schema attribution error=%v", err)
	}
}

func TestExactToolCostsAreMarginalAndCached(t *testing.T) {
	var templateCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apply-template":
			templateCalls.Add(1)
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			messages := body["messages"].([]any)
			system := messages[0].(map[string]any)["content"].(string)
			prompt := system
			if tools, present := body["tools"]; present {
				prompt += "|shared-tool-template|"
				for _, raw := range tools.([]any) {
					prompt += schemaName(raw.(map[string]any)) + "|schema|"
				}
			}
			data, _ := json.Marshal(map[string]string{"prompt": prompt})
			_, _ = w.Write(data)
		case "/tokenize":
			var body struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			tokens := make([]int, len(body.Content))
			data, _ := json.Marshal(map[string]any{"tokens": tokens})
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	profile := config.Profile{BaseURL: server.URL, RequestTimeoutS: 5, Capabilities: config.Capabilities{Tokenize: true, ApplyTemplate: true, ApplyTemplateTools: true}}
	item := &session.Session{ID: "cache", SchemaTokens: map[string]int{}, MarginalTokens: map[string]int{}}
	one, two := testSchema("one"), testSchema("two")
	input := budgetInput{
		SystemBase:         "system",
		System:             "system tools one, two",
		WithoutToolSystems: map[string]string{"one": "system tools two", "two": "system tools one"},
		Schemas:            []any{one, two},
		AllSchemas:         map[string]any{"one": one, "two": two},
	}
	budgeter := NewBudgeter()
	first, err := budgeter.Measure(context.Background(), &profile, item, config.GlobalContext{}, input, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := templateCalls.Load(); got != 7 {
		t.Fatalf("first template renders=%d, want 7 (3 budget + 2 isolated + 2 marginal)", got)
	}
	if first.ToolMarginalTokens["one"] <= 0 || first.ToolMarginalTokens["two"] <= 0 {
		t.Fatalf("marginal costs=%v", first.ToolMarginalTokens)
	}
	if first.ToolSchemaTokens["one"] <= first.ToolMarginalTokens["one"] {
		t.Fatalf("isolated schema should include more shared overhead: schema=%v marginal=%v", first.ToolSchemaTokens, first.ToolMarginalTokens)
	}
	budgeter.MarkRequest(item.ID, first.UsedEst)
	budgeter.RecordUsage(item.ID, first.UsedEst+5, -1)
	second, err := budgeter.Measure(context.Background(), &profile, item, config.GlobalContext{}, input, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := templateCalls.Load(); got != 10 {
		t.Fatalf("cached template renders=%d, want 10 (three regular renders added)", got)
	}
	if fmt.Sprint(second.ToolMarginalTokens) != fmt.Sprint(first.ToolMarginalTokens) {
		t.Fatalf("cached marginals changed: first=%v second=%v", first.ToolMarginalTokens, second.ToolMarginalTokens)
	}

	input.System = "system tools one"
	input.WithoutToolSystems = map[string]string{"one": "system tools "}
	input.Schemas = []any{one}
	third, err := budgeter.Measure(context.Background(), &profile, item, config.GlobalContext{}, input, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := templateCalls.Load(); got != 16 {
		t.Fatalf("tool-change template renders=%d, want 16 (3 budget + 2 isolated + 1 marginal added)", got)
	}
	if _, found := third.ToolMarginalTokens["two"]; found {
		t.Fatalf("disabled tool acquired a marginal request cost: %v", third.ToolMarginalTokens)
	}
}

func testSchema(name string) map[string]any {
	return map[string]any{"type": "function", "function": map[string]any{"name": name, "description": "test", "parameters": map[string]any{"type": "object"}}}
}

func schemaName(schema map[string]any) string {
	function, _ := schema["function"].(map[string]any)
	name, _ := function["name"].(string)
	return name
}
