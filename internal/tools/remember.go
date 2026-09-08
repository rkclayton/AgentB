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
	return "Save durable memory: use workspace for project facts and agent for operator preferences, style, or standing working habits; call recall first to avoid duplicates."
}
func (*Remember) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"note": map[string]any{"type": "string"}, "target": map[string]any{"type": "string", "enum": []string{"workspace", "agent"}, "default": "workspace"}}, "required": []string{"note"}}
}
func (r *Remember) Call(ctx context.Context, s *session.Session, args map[string]any) (string, error) {
	note, ok := args["note"].(string)
	if !ok {
		return "", fmt.Errorf("note is empty")
	}
	target, _ := args["target"].(string)
	if target == "" {
		target = "workspace"
	}
	var path string
	var duplicate bool
	var err error
	if target == "agent" {
		path, duplicate, err = r.memory.NoteAgent(s.AgentID, note)
	} else if target == "workspace" {
		path, duplicate, err = r.memory.Note(s.Workspace, note)
	} else {
		return "", fmt.Errorf("target must be workspace or agent")
	}
	if err != nil {
		return "", err
	}
	if duplicate {
		return "ok: already noted", nil
	}
	r.bus.Publish(events.New(events.MemoryNoted, s.ID, "", map[string]any{"note": note, "path": path, "target": target, "agent_id": s.AgentID}))
	return "ok: noted; active next session.", nil
}
