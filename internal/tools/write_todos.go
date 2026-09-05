package tools

import (
	"context"
	"fmt"
	"math"
	"strings"

	"harness/internal/events"
	"harness/internal/session"
)

type WriteTodos struct {
	bus *events.Bus
}

func NewWriteTodos(bus *events.Bus) *WriteTodos { return &WriteTodos{bus: bus} }
func (*WriteTodos) Name() string                { return "write_todos" }
func (*WriteTodos) Description() string {
	return "Set todos, or update one item's status by 1-based index. Use for multi-step work with distinct stages, not single-step tasks."
}
func (*WriteTodos) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"todos":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"index":  map[string]any{"type": "integer", "minimum": 1},
			"status": map[string]any{"type": "string", "enum": []string{"pending", "in progress", "done"}},
		},
		"anyOf": []map[string]any{
			{"required": []string{"todos"}},
			{"required": []string{"index", "status"}},
		},
	}
}
func (w *WriteTodos) Call(_ context.Context, s *session.Session, args map[string]any) (string, error) {
	if raw, set := args["todos"]; set {
		if _, hasIndex := args["index"]; hasIndex {
			return "", fmt.Errorf("provide todos to set the list, or index and status to update one item")
		}
		if _, hasStatus := args["status"]; hasStatus {
			return "", fmt.Errorf("provide todos to set the list, or index and status to update one item")
		}
		values, ok := raw.([]any)
		if !ok {
			return "", fmt.Errorf("todos must be an array of strings")
		}
		texts := make([]string, len(values))
		for index, value := range values {
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return "", fmt.Errorf("todo %d must be a non-empty string", index+1)
			}
			texts[index] = strings.TrimSpace(text)
		}
		todos := s.ReplaceTodos(texts)
		w.publish(s, todos, "set", 0)
		return renderTodos(todos), nil
	}

	indexValue, hasIndex := args["index"]
	status, hasStatus := args["status"].(string)
	index, ok := todoIndex(indexValue)
	if !hasIndex || !ok || !hasStatus {
		return "", fmt.Errorf("provide todos to set the list, or index and status to update one item")
	}
	if status != "pending" && status != "in progress" && status != "done" {
		return "", fmt.Errorf("status must be pending, in progress, or done")
	}
	todos, found := s.UpdateTodo(index, status)
	if !found {
		return "", fmt.Errorf("todo index %d is out of range", index)
	}
	w.publish(s, todos, "status", index)
	return renderTodos(todos), nil
}

func (w *WriteTodos) publish(s *session.Session, todos []session.Todo, operation string, index int) {
	data := map[string]any{"todos": todos, "operation": operation}
	if index > 0 {
		data["index"] = index
	}
	w.bus.Publish(events.New(events.TodosUpdated, s.ID, s.Snapshot(nil).Run.RunID, data))
}

func todoIndex(value any) (int, bool) {
	number, ok := value.(float64)
	if !ok || number < 1 || number != math.Trunc(number) || number > float64(math.MaxInt) {
		return 0, false
	}
	return int(number), true
}

func renderTodos(todos []session.Todo) string {
	if len(todos) == 0 {
		return "Todos cleared."
	}
	var out strings.Builder
	out.WriteString("Todos:\n")
	for index, item := range todos {
		fmt.Fprintf(&out, "%d. [%s] %s\n", index+1, item.Status, item.Text)
	}
	return strings.TrimRight(out.String(), "\n")
}
