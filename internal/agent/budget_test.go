package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestColdPrefillSuppressesImmediateCompaction(t *testing.T) {
	budget := NewBudgeter()
	budget.RecordUsage("session", 20000, 1661)
	if !budget.ColdPrefill("session") {
		t.Fatal("cold prefill was not recognized")
	}
	budget.RecordUsage("session", 20000, 18000)
	if budget.ColdPrefill("session") {
		t.Fatal("warm prefix was classified cold")
	}
}

func TestExactAccountingUsesProfileTimeoutAfterConnect(t *testing.T) {
	var slow sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apply-template":
			slow.Do(func() { time.Sleep(2700 * time.Millisecond) })
			_, _ = w.Write([]byte(`{"prompt":"rendered"}`))
		case "/tokenize":
			_, _ = w.Write([]byte(`{"tokens":[1]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	profile := config.Profile{BaseURL: server.URL, RequestTimeoutS: 5, Capabilities: config.Capabilities{Tokenize: true, ApplyTemplate: true, ApplyTemplateTools: true}}
	item := &session.Session{ID: "normal-timeout", SchemaTokens: map[string]int{}, MarginalTokens: map[string]int{}}
	started := time.Now()
	if _, err := NewBudgeter().Measure(context.Background(), &profile, item, config.GlobalContext{}, budgetInput{SystemBase: "system", System: "system"}, false); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 2500*time.Millisecond {
		t.Fatalf("accounting returned before the old fast-fail boundary: %s", elapsed)
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
	if got := templateCalls.Load(); got != 8 {
		t.Fatalf("first template renders=%d, want 8 (4 budget + 2 isolated + 2 marginal)", got)
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
	if got := templateCalls.Load(); got != 12 {
		t.Fatalf("cached template renders=%d, want 12 (four regular renders added)", got)
	}
	if fmt.Sprint(second.ToolMarginalTokens) != fmt.Sprint(first.ToolMarginalTokens) {
		t.Fatalf("cached marginals changed: first=%v second=%v", first.ToolMarginalTokens, second.ToolMarginalTokens)
	}
	budgeter.InvalidateToolCosts()
	_, err = budgeter.Measure(context.Background(), &profile, item, config.GlobalContext{}, input, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := templateCalls.Load(); got != 20 {
		t.Fatalf("one prefix invalidation rendered costs again: calls=%d, want 20", got)
	}

	input.System = "system tools one"
	input.WithoutToolSystems = map[string]string{"one": "system tools "}
	input.Schemas = []any{one}
	third, err := budgeter.Measure(context.Background(), &profile, item, config.GlobalContext{}, input, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := templateCalls.Load(); got != 27 {
		t.Fatalf("tool-change template renders=%d, want 27 (4 budget + 2 isolated + 1 marginal added)", got)
	}
	if _, found := third.ToolMarginalTokens["two"]; found {
		t.Fatalf("disabled tool acquired a marginal request cost: %v", third.ToolMarginalTokens)
	}
}

func TestMessageWeightsCacheByIDAndInvalidateOnContentChange(t *testing.T) {
	var weightCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apply-template":
			var body struct {
				Messages []llm.Message `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			data, _ := json.Marshal(map[string]string{"prompt": strings.Repeat("p", len(body.Messages)*10)})
			_, _ = w.Write(data)
		case "/tokenize":
			var body struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if strings.HasPrefix(body.Content, "weight-") || body.Content == "[elided]" {
				weightCalls.Add(1)
			}
			tokens := make([]int, max(1, len(body.Content)))
			data, _ := json.Marshal(map[string]any{"tokens": tokens})
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	profile := config.Profile{BaseURL: server.URL, Model: "model", RequestTimeoutS: 5, Capabilities: config.Capabilities{Tokenize: true, ApplyTemplate: true}}
	item := &session.Session{ID: "message-cache", SchemaTokens: map[string]int{}, MarginalTokens: map[string]int{}}
	input := budgetInput{SystemBase: "system", System: "system"}
	for index := 0; index < 19; index++ {
		content := fmt.Sprintf("weight-%02d", index)
		input.Messages = append(input.Messages, llm.Message{Role: "user", Content: content})
		input.Records = append(input.Records, events.Message{ID: fmt.Sprintf("m-%02d", index), Role: "user", Content: content, Category: "history"})
	}
	budgeter := NewBudgeter()
	measure := func() {
		t.Helper()
		if _, err := budgeter.Measure(context.Background(), &profile, item, config.GlobalContext{}, input, false); err != nil {
			t.Fatal(err)
		}
	}
	measure()
	if got := weightCalls.Load(); got != 19 {
		t.Fatalf("cold weight calls=%d, want 19", got)
	}
	measure()
	if got := weightCalls.Load(); got != 19 {
		t.Fatalf("warm weight calls=%d, want no new calls", got)
	}

	input.Messages = append(input.Messages, llm.Message{Role: "user", Content: "weight-new"})
	input.Records = append(input.Records, events.Message{ID: "m-new", Role: "user", Content: "weight-new", Category: "history"})
	measure()
	if got := weightCalls.Load(); got != 20 {
		t.Fatalf("appended-message weight calls=%d, want one new call", got)
	}

	input.Messages[0].Content, input.Records[0].Content = "weight-edited", "weight-edited"
	measure()
	if got := weightCalls.Load(); got != 21 {
		t.Fatalf("edited-message weight calls=%d, want one invalidated call", got)
	}
	input.Messages[1].Content, input.Records[1].Content = "[elided]", "[elided]"
	measure()
	if got := weightCalls.Load(); got != 22 {
		t.Fatalf("elided-message weight calls=%d, want one invalidated call", got)
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
