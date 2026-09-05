package tools

import (
	"context"
	"strings"
	"testing"

	"harness/internal/events"
	"harness/internal/session"
)

func TestWriteTodosSetsAndUpdatesSessionState(t *testing.T) {
	bus := events.NewBus()
	tool := NewWriteTodos(bus)
	item := &session.Session{ID: "main", ToolsEnabled: map[string]bool{"write_todos": true}}

	result, err := tool.Call(context.Background(), item, map[string]any{"todos": []any{"Inspect the code", "Implement the change", "Verify it"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "1. [pending] Inspect the code") || len(item.Snapshot(nil).Todos) != 3 {
		t.Fatalf("set result=%q snapshot=%+v", result, item.Snapshot(nil).Todos)
	}
	result, err = tool.Call(context.Background(), item, map[string]any{"index": float64(1), "status": "in progress"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "1. [in progress] Inspect the code") || item.Snapshot(nil).Todos[0].Status != "in progress" {
		t.Fatalf("update result=%q snapshot=%+v", result, item.Snapshot(nil).Todos)
	}
	recent := bus.Recent("main")
	if len(recent) != 2 || recent[0].Type != events.TodosUpdated || recent[1].Type != events.TodosUpdated {
		t.Fatalf("events=%+v", recent)
	}
}

func TestWriteTodosReplacesListAndRejectsInvalidUpdates(t *testing.T) {
	tool := NewWriteTodos(events.NewBus())
	item := &session.Session{ID: "main"}
	if _, err := tool.Call(context.Background(), item, map[string]any{"todos": []any{"First"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Call(context.Background(), item, map[string]any{"index": float64(2), "status": "done"}); err == nil {
		t.Fatal("out-of-range update succeeded")
	}
	if _, err := tool.Call(context.Background(), item, map[string]any{"index": float64(1), "status": "blocked"}); err == nil {
		t.Fatal("unsupported status succeeded")
	}
	if _, err := tool.Call(context.Background(), item, map[string]any{"todos": []any{"Replacement"}}); err != nil {
		t.Fatal(err)
	}
	todos := item.Snapshot(nil).Todos
	if len(todos) != 1 || todos[0].Text != "Replacement" || todos[0].Status != "pending" {
		t.Fatalf("replacement=%+v", todos)
	}
}

func TestWriteTodosSchemaCarriesOnlyTodoGuidance(t *testing.T) {
	tool := NewWriteTodos(events.NewBus())
	description := tool.Description()
	if !strings.Contains(description, "multi-step work with distinct stages") || !strings.Contains(description, "not single-step tasks") {
		t.Fatalf("description=%q", description)
	}
	if strings.Contains(strings.ToLower(description), "plan") {
		t.Fatalf("description uses banned naming: %q", description)
	}
}
