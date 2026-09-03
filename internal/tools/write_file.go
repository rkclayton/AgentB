package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"harness/internal/events"
	"harness/internal/session"
)

type FileCoordinator struct {
	workspaces *session.WorkspaceRegistry
	label      func(string) string
	bus        *events.Bus
}

func NewFileCoordinator(workspaces *session.WorkspaceRegistry, label func(string) string, bus *events.Bus) *FileCoordinator {
	return &FileCoordinator{workspaces: workspaces, label: label, bus: bus}
}
func cleanRel(path string) string { return filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))) }
func workspaceRel(workspace, resolved string) string {
	rel, err := filepath.Rel(workspace, resolved)
	if err != nil {
		return cleanRel(resolved)
	}
	return cleanRel(rel)
}
func (c *FileCoordinator) check(s *session.Session, path, resolved string) (string, error) {
	rel := workspaceRel(s.Workspace, resolved)
	seen, hasSeen := s.LastSeenAt(rel)
	if record, ok := c.workspaces.LastWriter(s.Workspace, rel); ok && record.SessionID != s.ID && (!hasSeen || record.At.After(seen)) {
		age := int(time.Since(record.At).Seconds())
		if age < 0 {
			age = 0
		}
		label := c.label(record.SessionID)
		c.bus.Publish(events.New(events.WorkspaceConflict, s.ID, "", map[string]any{"path": rel, "session_id": s.ID, "other_session_id": record.SessionID, "other_label": label, "age_s": age}))
		return "", fmt.Errorf("session %s wrote this file %ds ago; re-read before editing.", label, age)
	}
	if info, err := os.Stat(resolved); err == nil && hasSeen && info.ModTime().After(seen) {
		return "note: file changed since you last read it.\n", nil
	}
	return "", nil
}
func (c *FileCoordinator) record(s *session.Session, resolved string) {
	rel := workspaceRel(s.Workspace, resolved)
	s.Touch(rel)
	c.workspaces.RecordWrite(s.Workspace, rel, s.ID)
}

type WriteFile struct{ coordinator *FileCoordinator }

func NewWriteFile(c *FileCoordinator) *WriteFile { return &WriteFile{coordinator: c} }
func (*WriteFile) Name() string                  { return "write_file" }
func (*WriteFile) Description() string {
	return "Create or overwrite a file with the given content, creating parent folders. For changes inside an existing file use edit_file."
}
func (*WriteFile) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}}, "required": []string{"path", "content"}}
}
func (w *WriteFile) Call(ctx context.Context, s *session.Session, args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return "", fmt.Errorf("path is required")
	}
	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content is required")
	}
	if len(content) > 512*1024 {
		return "", fmt.Errorf("content exceeds the 512 KB limit")
	}
	resolved, err := Resolve(s.Workspace, path)
	if err != nil {
		return "", err
	}
	prefix, err := w.coordinator.check(s, path, resolved)
	if err != nil {
		return "", err
	}
	fail := func(cause error) (string, error) {
		if prefix != "" {
			return "", fmt.Errorf("%serror: %v", prefix, cause)
		}
		return "", cause
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return fail(err)
	}
	temp, err := os.CreateTemp(filepath.Dir(resolved), ".agentb-write-*")
	if err != nil {
		return fail(err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err = temp.WriteString(content); err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fail(err)
	}
	if err := atomicReplace(tempPath, resolved); err != nil {
		return fail(err)
	}
	w.coordinator.record(s, resolved)
	lines := 0
	if content != "" {
		lines = strings.Count(content, "\n") + 1
		if strings.HasSuffix(content, "\n") {
			lines--
		}
	}
	return prefix + fmt.Sprintf("ok: wrote %s (%d lines)", cleanRel(path), lines), nil
}
