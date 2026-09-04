package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"

	"harness/internal/config"
	"harness/internal/session"
)

type ReadFile struct {
	mu  sync.RWMutex
	cfg config.ReadFileTool
}

func NewReadFile(cfg config.ReadFileTool) *ReadFile { return &ReadFile{cfg: cfg} }
func (*ReadFile) Name() string                      { return "read_file" }
func (*ReadFile) Description() string {
	return "Read local UTF-8 text by byte offset and limit. Unlike fetch_url, it reads the filesystem."
}
func (r *ReadFile) Schema() map[string]any {
	cfg := r.config()
	defaultLimit := min(cfg.DefaultLimit, cfg.MaxLimit)
	return map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "offset": map[string]any{"type": "integer", "description": "One-based byte offset", "default": 1}, "limit": map[string]any{"type": "integer", "description": "Maximum bytes to return", "default": defaultLimit, "maximum": cfg.MaxLimit}}, "required": []string{"path"}}
}
func (r *ReadFile) Call(ctx context.Context, s *session.Session, args map[string]any) (string, error) {
	cfg := r.config()
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return "", fmt.Errorf("path is required")
	}
	resolved, err := resolveForTool(ctx, s.Workspace, path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", err
	}
	sample := data
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return "", fmt.Errorf("binary file refused: %s", path)
	}
	offset := number(args["offset"], 1)
	limit := number(args["limit"], cfg.DefaultLimit)
	limit = min(limit, cfg.MaxLimit)
	window, err := windowUTF8(string(data), offset, limit)
	if err != nil {
		return "", err
	}
	result := byteWindowHeader(window) + "\n" + window.Content
	s.Touch(workspaceRel(s.Workspace, resolved))
	return result, nil
}
func (r *ReadFile) Configure(value config.Config) {
	r.mu.Lock()
	r.cfg = value.Tools.ReadFile
	r.mu.Unlock()
}
func (r *ReadFile) config() config.ReadFileTool { r.mu.RLock(); defer r.mu.RUnlock(); return r.cfg }
func number(value any, fallback int) int {
	if value == nil {
		return fallback
	}
	if n, ok := value.(float64); ok {
		return int(n)
	}
	if n, ok := value.(int); ok {
		return n
	}
	return fallback
}
