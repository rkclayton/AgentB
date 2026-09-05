package events

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Writers struct {
	mu         sync.Mutex
	dir, start string
	global     *os.File
	sessions   map[string]*os.File
}

func NewWriters(dir string) (*Writers, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	stamp := time.Now().UTC().Format("20060102T150405.000Z")
	file, err := os.OpenFile(filepath.Join(dir, "Agent_b-"+stamp+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &Writers{dir: dir, start: stamp, global: file, sessions: map[string]*os.File{}}, nil
}
func (w *Writers) OpenSession(id string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if old := w.sessions[id]; old != nil {
		if err := old.Sync(); err != nil {
			return "", err
		}
		if err := old.Close(); err != nil {
			return "", err
		}
	}
	path := filepath.Join(w.dir, fmt.Sprintf("%s-%s.jsonl", id, time.Now().UTC().Format("20060102T150405.000Z")))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	w.sessions[id] = file
	return path, nil
}
func (w *Writers) CloseSession(id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	file := w.sessions[id]
	if file == nil {
		return nil
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if closeErr == nil {
		delete(w.sessions, id)
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
func (w *Writers) Write(event Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	file := w.global
	if event.SessionID != "" && w.sessions[event.SessionID] != nil {
		file = w.sessions[event.SessionID]
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	if event.Type == "run.stopped" {
		return file.Sync()
	}
	return nil
}
func (w *Writers) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var first error
	for _, file := range append([]*os.File{w.global}, mapFiles(w.sessions)...) {
		if file == nil {
			continue
		}
		if err := file.Sync(); err != nil && first == nil {
			first = err
		}
		if err := file.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
func mapFiles(values map[string]*os.File) []*os.File {
	out := make([]*os.File, 0, len(values))
	for _, file := range values {
		out = append(out, file)
	}
	return out
}
