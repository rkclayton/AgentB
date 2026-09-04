package tools

import (
	"context"

	"harness/internal/memory"
	"harness/internal/session"
)

type Recall struct {
	memory *memory.Manager
}

func NewRecall(manager *memory.Manager) *Recall { return &Recall{memory: manager} }
func (*Recall) Name() string                    { return "recall" }
func (*Recall) Description() string {
	return "Read every durable note stored for this workspace."
}
func (*Recall) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (r *Recall) Call(_ context.Context, s *session.Session, _ map[string]any) (string, error) {
	content, err := r.memory.Read(s.Workspace)
	if err != nil {
		return "", err
	}
	if content == "" {
		return "No saved notes for this workspace.", nil
	}
	return content, nil
}
