package tools

import (
	"context"
	"fmt"

	"harness/internal/events"
	"harness/internal/memory"
	"harness/internal/session"
)

type Remember struct {
	memory *memory.Manager
	bus    *events.Bus
}

func NewRemember(manager *memory.Manager, bus *events.Bus) *Remember {
	return &Remember{memory: manager, bus: bus}
}
func (*Remember) Name() string { return "remember" }
func (*Remember) Description() string {
	return "Save note as durable workspace memory for future sessions. Call recall first to avoid duplicates; unlike recall, remember writes."
}
func (*Remember) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"note": map[string]any{"type": "string"}}, "required": []string{"note"}}
}
func (r *Remember) Call(ctx context.Context, s *session.Session, args map[string]any) (string, error) {
	note, ok := args["note"].(string)
	if !ok {
		return "", fmt.Errorf("note is empty")
	}
	path, duplicate, err := r.memory.Note(s.Workspace, note)
	if err != nil {
		return "", err
	}
	if duplicate {
		return "ok: already noted", nil
	}
	r.bus.Publish(events.New(events.MemoryNoted, s.ID, "", map[string]any{"note": note, "path": path}))
	return "ok: noted; active next session.", nil
}
