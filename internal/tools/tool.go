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

// CallOutcome carries dispatcher-only control information that is deliberately
// absent from the model-facing tool schema and result text.
type CallOutcome struct {
	Content                   string
	OK                        bool
	OperatorOverrideAvailable bool
	OperatorOverrideReason    string
}

// DetailedTool is optional. It lets a tool report that a narrowly scoped,
// user-approved retry is available without exposing that retry to the model.
type DetailedTool interface {
	CallDetailed(context.Context, *session.Session, map[string]any) CallDetail
}

type CallDetail struct {
	Content                string
	Err                    error
	OperatorOverrideReason string
}

// OperatorOverrideTool is optional and is called only by the dispatcher after
// its unconditional approval gate succeeds.
type OperatorOverrideTool interface {
	CallAsOperator(context.Context, *session.Session, map[string]any) (string, error)
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
	outcome := r.CallDetailed(ctx, s, name, args)
	return outcome.Content, outcome.OK
}
func (r *Registry) CallDetailed(ctx context.Context, s *session.Session, name string, args map[string]any) CallOutcome {
	tool := r.byName[name]
	if tool == nil || !s.ToolEnabled(name) {
		return CallOutcome{Content: fmt.Sprintf("error: tool %s is not available", name)}
	}
	var result string
	var err error
	overrideReason := ""
	if detailed, ok := tool.(DetailedTool); ok {
		detail := detailed.CallDetailed(ctx, s, args)
		result, err, overrideReason = detail.Content, detail.Err, detail.OperatorOverrideReason
	} else {
		result, err = tool.Call(ctx, s, args)
	}
	if err != nil {
		if strings.HasPrefix(err.Error(), "note:") {
			return CallOutcome{Content: err.Error()}
		}
		return CallOutcome{Content: "error: " + err.Error()}
	}
	return CallOutcome{Content: result, OK: true, OperatorOverrideAvailable: overrideReason != "", OperatorOverrideReason: overrideReason}
}
func (r *Registry) CallAsOperator(ctx context.Context, s *session.Session, name string, args map[string]any) (string, bool) {
	tool := r.byName[name]
	if tool == nil || !s.ToolEnabled(name) {
		return fmt.Sprintf("error: tool %s is not available", name), false
	}
	override, ok := tool.(OperatorOverrideTool)
	if !ok {
		return fmt.Sprintf("error: tool %s has no operator-identity override", name), false
	}
	result, err := override.CallAsOperator(ctx, s, args)
	if err != nil {
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
