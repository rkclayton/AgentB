package session

import (
	"path/filepath"
	"sync"
	"time"
)

type WriteRecord struct {
	SessionID string
	At        time.Time
}
type WorkspaceRegistry struct {
	mu         sync.Mutex
	lastWriter map[string]map[string]WriteRecord
}

func NewWorkspaceRegistry() *WorkspaceRegistry {
	return &WorkspaceRegistry{lastWriter: map[string]map[string]WriteRecord{}}
}
func (w *WorkspaceRegistry) LastWriter(workspace, path string) (WriteRecord, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	record, ok := w.lastWriter[clean(workspace)][filepath.ToSlash(filepath.Clean(path))]
	return record, ok
}
func (w *WorkspaceRegistry) RecordWrite(workspace, path, sessionID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	root := clean(workspace)
	if w.lastWriter[root] == nil {
		w.lastWriter[root] = map[string]WriteRecord{}
	}
	w.lastWriter[root][filepath.ToSlash(filepath.Clean(path))] = WriteRecord{SessionID: sessionID, At: time.Now().UTC()}
}
func clean(path string) string { abs, _ := filepath.Abs(path); return filepath.Clean(abs) }
