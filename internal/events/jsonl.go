package events

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Writers struct {
	mu         sync.Mutex
	dir, start string
	global     *os.File
	sessions   map[string]*os.File
	paths      map[string]string
	sizes      map[string]int64
	history    map[string]*historyIndex
}

// LogCursor identifies a complete record boundary in one JSONL generation.
type LogCursor struct {
	Generation string `json:"generation"`
	Offset     int64  `json:"offset"`
	Path       string `json:"-"`
}

type MemoryWrite struct {
	Path    string `json:"path"`
	Note    string `json:"note"`
	Target  string `json:"target"`
	AgentID string `json:"agent_id,omitempty"`
}

type SessionInventory struct {
	Events       int           `json:"events"`
	JSONLFiles   int           `json:"jsonl_files"`
	JSONLBytes   int64         `json:"jsonl_bytes"`
	MemoryWrites []MemoryWrite `json:"memory_writes"`
	Paths        []string      `json:"-"`
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
	return &Writers{dir: dir, start: stamp, global: file, sessions: map[string]*os.File{}, paths: map[string]string{}, sizes: map[string]int64{}, history: map[string]*historyIndex{}}, nil
}
func (w *Writers) OpenSession(id string) (string, error) {
	path, _, err := w.RotateSession(id)
	return path, err
}

// RotateSession opens a new generation and returns the durable end of its predecessor.
// An empty predecessor is meaningful for a session's first generation.
func (w *Writers) RotateSession(id string) (string, LogCursor, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	predecessor := LogCursor{Generation: filepath.Base(w.paths[id]), Offset: w.sizes[id], Path: w.paths[id]}
	if old := w.sessions[id]; old != nil {
		if err := old.Sync(); err != nil {
			return "", LogCursor{}, err
		}
		if err := old.Close(); err != nil {
			return "", LogCursor{}, err
		}
	}
	path := filepath.Join(w.dir, fmt.Sprintf("%s-%s.jsonl", id, time.Now().UTC().Format("20060102T150405.000000000Z")))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return "", LogCursor{}, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return "", LogCursor{}, err
	}
	w.sessions[id] = file
	w.paths[id] = path
	w.sizes[id] = info.Size()
	w.history[id] = newHistoryIndex()
	return path, predecessor, nil
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
		delete(w.paths, id)
		delete(w.sizes, id)
		delete(w.history, id)
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func (w *Writers) SessionInventory(id string) (SessionInventory, error) {
	w.mu.Lock()
	if file := w.sessions[id]; file != nil {
		if err := file.Sync(); err != nil {
			w.mu.Unlock()
			return SessionInventory{}, err
		}
	}
	dir := w.dir
	w.mu.Unlock()
	matches, err := filepath.Glob(filepath.Join(dir, id+"-*.jsonl"))
	if err != nil {
		return SessionInventory{}, err
	}
	result := SessionInventory{Paths: []string{}, MemoryWrites: []MemoryWrite{}}
	for _, path := range matches {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return SessionInventory{}, statErr
		}
		if !info.Mode().IsRegular() {
			continue
		}
		result.Paths = append(result.Paths, path)
		result.JSONLFiles++
		result.JSONLBytes += info.Size()
		file, openErr := os.Open(path)
		if openErr != nil {
			return SessionInventory{}, openErr
		}
		decoder := json.NewDecoder(file)
		for {
			var event Event
			decodeErr := decoder.Decode(&event)
			if decodeErr == io.EOF {
				break
			}
			if decodeErr != nil {
				_ = file.Close()
				return SessionInventory{}, fmt.Errorf("inventory %s: %w", path, decodeErr)
			}
			if event.SessionID == id && event.Type == MemoryNoted {
				data := valueMap(event.Data)
				result.MemoryWrites = append(result.MemoryWrites, MemoryWrite{Path: valueString(data["path"]), Note: valueString(data["note"]), Target: valueString(data["target"]), AgentID: valueString(data["agent_id"])})
			}
			if event.SessionID == id {
				result.Events++
			}
		}
		if closeErr := file.Close(); closeErr != nil {
			return SessionInventory{}, closeErr
		}
	}
	sort.Strings(result.Paths)
	return result, nil
}

func (w *Writers) DeleteSession(id string) (SessionInventory, error) {
	inventory, err := w.SessionInventory(id)
	if err != nil {
		return SessionInventory{}, err
	}
	if err := w.CloseSession(id); err != nil {
		return SessionInventory{}, err
	}
	for _, path := range inventory.Paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return SessionInventory{}, err
		}
	}
	return inventory, nil
}
func (w *Writers) Write(event Event) error {
	_, err := w.WriteRecord(event)
	return err
}

// WriteRecord returns a cursor only after a complete JSONL record was appended.
func (w *Writers) WriteRecord(event Event) (LogCursor, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	file := w.global
	path := ""
	if event.SessionID != "" && w.sessions[event.SessionID] != nil {
		file = w.sessions[event.SessionID]
		path = w.paths[event.SessionID]
	}
	data, err := json.Marshal(event)
	if err != nil {
		return LogCursor{}, err
	}
	line := append(data, '\n')
	offset := w.sizes[event.SessionID]
	written, err := file.Write(line)
	if err != nil {
		return LogCursor{}, err
	}
	if written != len(line) {
		return LogCursor{}, io.ErrShortWrite
	}
	if event.SessionID != "" && file == w.sessions[event.SessionID] {
		w.sizes[event.SessionID] += int64(written)
	}
	if index := w.history[event.SessionID]; index != nil {
		index.record(event, historyLocation{path: w.paths[event.SessionID], offset: offset, length: written})
	}
	if event.Type == "run.stopped" {
		if err := file.Sync(); err != nil {
			return LogCursor{}, err
		}
	}
	if event.SessionID == "" || path == "" {
		return LogCursor{}, nil
	}
	return LogCursor{Generation: filepath.Base(path), Offset: offset + int64(written), Path: path}, nil
}

func (w *Writers) SessionCursors() map[string]LogCursor {
	w.mu.Lock()
	defer w.mu.Unlock()
	result := make(map[string]LogCursor, len(w.paths))
	for id, path := range w.paths {
		result[id] = LogCursor{Generation: filepath.Base(path), Offset: w.sizes[id], Path: path}
	}
	return result
}

func (w *Writers) ResolveHistory(sessionID, ref string) (HistoryLookup, error) {
	w.mu.Lock()
	index := w.history[sessionID]
	if index == nil {
		w.mu.Unlock()
		return HistoryLookup{}, fmt.Errorf("history is not available for this session")
	}
	lookup, err := index.resolve(ref, func(record historyRecord) (Message, error) {
		return Message{}, nil
	})
	if err != nil || lookup.Kind == "manifest" {
		w.mu.Unlock()
		return lookup, err
	}
	record := index.records[ref]
	w.mu.Unlock()
	message, err := readHistoryMessage(record.location)
	if err != nil {
		return HistoryLookup{}, err
	}
	if message.ID != ref {
		return HistoryLookup{}, fmt.Errorf("recorded history ref mismatch: got %q, want %q", message.ID, ref)
	}
	lookup.Message = message
	return lookup, nil
}

func (h *historyIndex) record(event Event, location historyLocation) {
	data := valueMap(event.Data)
	switch event.Type {
	case MessageAppended:
		var wrapper struct {
			Message Message `json:"message"`
		}
		if decodeValue(event.Data, &wrapper) == nil {
			h.append(wrapper.Message, location, false)
		}
	case MessageUpdated:
		h.update(valueString(data["id"]), valueMap(data["patch"]))
	case MessageRemoved:
		h.remove(valueString(data["id"]))
	case Compaction:
		if valueString(data["kind"]) == "summarize" {
			h.compact(valueString(data["summary_message_id"]), valueStrings(data["affected_ids"]))
		}
	}
}

func readHistoryMessage(location historyLocation) (Message, error) {
	file, err := os.Open(location.path)
	if err != nil {
		return Message{}, err
	}
	defer file.Close()
	data := make([]byte, location.length)
	if _, err := file.ReadAt(data, location.offset); err != nil && err != io.EOF {
		return Message{}, err
	}
	var event Event
	if err := json.Unmarshal(data, &event); err != nil {
		return Message{}, err
	}
	if event.Type != MessageAppended {
		return Message{}, fmt.Errorf("recorded history location is %q, not %q", event.Type, MessageAppended)
	}
	var wrapper struct {
		Message Message `json:"message"`
	}
	if err := decodeValue(event.Data, &wrapper); err != nil {
		return Message{}, err
	}
	return wrapper.Message, nil
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
