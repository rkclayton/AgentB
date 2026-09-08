package tools

import (
	"context"
	"strings"

	"harness/internal/memory"
	"harness/internal/session"
)

type Recall struct {
	memory *memory.Manager
}

func NewRecall(manager *memory.Manager) *Recall { return &Recall{memory: manager} }
func (*Recall) Name() string                    { return "recall" }
func (*Recall) Description() string {
	return "Read durable workspace and agent notes; takes no arguments. Use before remember to avoid duplicates; recall never writes."
}
func (*Recall) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (r *Recall) Call(_ context.Context, s *session.Session, _ map[string]any) (string, error) {
	workspace, err := r.memory.Read(s.Workspace)
	if err != nil {
		return "", err
	}
	agent, err := r.memory.ReadAgent(s.AgentID)
	if err != nil {
		return "", err
	}
	if workspace == "" && agent == "" {
		return "No saved notes for this workspace.", nil
	}
	parts := []string{}
	if workspace != "" {
		parts = append(parts, "Workspace memory:\n"+workspace)
	}
	if agent != "" {
		parts = append(parts, "Agent memory:\n"+agent)
	}
	return strings.Join(parts, "\n\n"), nil
}
