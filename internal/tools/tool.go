package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"harness/internal/config"
	"harness/internal/session"
)

type Tool interface {
	Name() string
	Description() string
	Schema() map[string]any
	Call(context.Context, *session.Session, map[string]any) (string, error)
}
type Registry struct {
	ordered []Tool
	byName  map[string]Tool
}
type configurable interface{ Configure(config.Config) }

func New(items ...Tool) *Registry {
	r := &Registry{byName: map[string]Tool{}}
	for _, item := range items {
		r.ordered = append(r.ordered, item)
		r.byName[item.Name()] = item
	}
	return r
}
func (r *Registry) Configure(cfg config.Config) {
	for _, tool := range r.ordered {
		if item, ok := tool.(configurable); ok {
			item.Configure(cfg)
		}
	}
}
func (r *Registry) Names(enabled map[string]bool) []string {
	out := []string{}
	for _, tool := range r.ordered {
		if enabled[tool.Name()] {
			out = append(out, tool.Name())
		}
	}
	return out
}
func (r *Registry) Schemas(enabled map[string]bool) []any {
	out := []any{}
	for _, tool := range r.ordered {
		if enabled[tool.Name()] {
			out = append(out, map[string]any{"type": "function", "function": map[string]any{"name": tool.Name(), "description": tool.Description(), "parameters": tool.Schema()}})
		}
	}
	return out
}
func (r *Registry) AllSchemas() map[string]any {
	out := make(map[string]any, len(r.ordered))
	for _, tool := range r.ordered {
		out[tool.Name()] = map[string]any{"type": "function", "function": map[string]any{"name": tool.Name(), "description": tool.Description(), "parameters": tool.Schema()}}
	}
	return out
}
func (r *Registry) Call(ctx context.Context, s *session.Session, name string, args map[string]any) (string, bool) {
	tool := r.byName[name]
	if tool == nil || !s.ToolEnabled(name) {
		return fmt.Sprintf("error: tool %s is not available", name), false
	}
	result, err := tool.Call(ctx, s, args)
	if err != nil {
		if strings.HasPrefix(err.Error(), "note:") {
			return err.Error(), false
		}
		return "error: " + err.Error(), false
	}
	return result, true
}
func DecodeArgs(raw string) (map[string]any, error) {
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("invalid JSON arguments: %v", err)
	}
	return out, nil
}
