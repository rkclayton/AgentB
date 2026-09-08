package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
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
	return "Read numbered local UTF-8 text. Use byte mode with byte offset/limit, or line mode with one-based line/lines. In byte mode, when more is true, pass returned next_offset as offset to advance; in line mode, pass returned next_line as line. Unlike fetch_url, it reads the filesystem."
}
func (r *ReadFile) Schema() map[string]any {
	cfg := r.config()
	defaultLimit := min(cfg.DefaultLimit, cfg.MaxLimit)
	return map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "offset": map[string]any{"type": "integer", "description": "One-based byte offset; byte mode only", "default": 1}, "limit": map[string]any{"type": "integer", "description": "Maximum bytes to return; byte mode only", "default": defaultLimit, "maximum": cfg.MaxLimit}, "line": map[string]any{"type": "integer", "description": "One-based starting line; line mode only", "minimum": 1}, "lines": map[string]any{"type": "integer", "description": "Maximum lines to return; line mode only", "default": 200, "minimum": 1, "maximum": 2000}}, "required": []string{"path"}}
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
	if _, lineMode := args["line"]; lineMode {
		if _, byteOffset := args["offset"]; byteOffset {
			return "", fmt.Errorf("line mode cannot be combined with byte offset")
		}
		if _, byteLimit := args["limit"]; byteLimit {
			return "", fmt.Errorf("line mode cannot be combined with byte limit")
		}
		result, lineErr := numberedReadFileLines(string(data), number(args["line"], 1), number(args["lines"], 200))
		if lineErr != nil {
			return "", lineErr
		}
		s.Touch(workspaceRel(s.Workspace, resolved))
		return result, nil
	}
	if _, linesOnly := args["lines"]; linesOnly {
		return "", fmt.Errorf("lines requires line")
	}
	offset := number(args["offset"], 1)
	limit := number(args["limit"], cfg.DefaultLimit)
	limit = min(limit, cfg.MaxLimit)
	window, err := windowUTF8(string(data), offset, limit)
	if err != nil {
		return "", err
	}
	result := numberedReadFileWindow(string(data), window)
	s.Touch(workspaceRel(s.Workspace, resolved))
	return result, nil
}

func numberedReadFileLines(text string, line, count int) (string, error) {
	if line < 1 {
		return "", fmt.Errorf("line must be at least 1")
	}
	if count < 1 || count > 2000 {
		return "", fmt.Errorf("lines must be between 1 and 2000")
	}
	values := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		values = values[:len(values)-1]
	}
	total := len(values)
	start := line - 1
	if start > total {
		return "", fmt.Errorf("line %d is past end of file (%d lines)", line, total)
	}
	end := min(total, start+count)
	more := end < total
	header := fmt.Sprintf("[line window: line=%d lines=%d total_lines=%d more=%t", line, end-start, total, more)
	if more {
		header += fmt.Sprintf(" next_line=%d", end+1)
	}
	header += "]"
	if start == end {
		return header, nil
	}
	numbered := make([]string, 0, end-start)
	for index := start; index < end; index++ {
		numbered = append(numbered, fmt.Sprintf("%d: %s", index+1, strings.TrimSuffix(values[index], "\r")))
	}
	return header + "\n" + strings.Join(numbered, "\n"), nil
}

func numberedReadFileWindow(text string, window byteWindow) string {
	data := []byte(text)
	start := window.Offset - 1
	end := start + window.Bytes
	startLine := 1 + bytes.Count(data[:start], []byte{'\n'})
	startMidLine := start > 0 && data[start-1] != '\n'
	endMidLine := end < len(data) && (end == 0 || data[end-1] != '\n')
	header := byteWindowHeader(window)
	header = header[:len(header)-1] + fmt.Sprintf(" start_line=%d start_mid_line=%t end_mid_line=%t]", startLine, startMidLine, endMidLine)
	if window.Content == "" {
		return header
	}

	lines := strings.Split(window.Content, "\n")
	trailingNewline := strings.HasSuffix(window.Content, "\n")
	if trailingNewline {
		lines = lines[:len(lines)-1]
	}
	numbered := make([]string, 0, len(lines))
	for index, line := range lines {
		numbered = append(numbered, fmt.Sprintf("%d: %s", startLine+index, strings.TrimSuffix(line, "\r")))
	}
	body := strings.Join(numbered, "\n")
	if trailingNewline {
		body += "\n"
	}
	return header + "\n" + body
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
