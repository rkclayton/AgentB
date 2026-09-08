package projection

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"harness/internal/events"
)

// Store is a discardable live cache over JSONL. It has no persisted state: the first
// snapshot projects through a captured record boundary; later durable records are
// folded once and emitted as versioned projection patches.
type Store struct {
	mu          sync.Mutex
	cache       *Cache
	states      map[string]Snapshot
	sources     map[string]events.LogCursor
	initialized map[string]bool
	stale       map[string]string
	subscribers map[int]chan Patch
	next        int
}

func NewStore() *Store {
	return &Store{
		cache: NewCache(), states: map[string]Snapshot{}, sources: map[string]events.LogCursor{},
		initialized: map[string]bool{}, stale: map[string]string{}, subscribers: map[int]chan Patch{},
	}
}

func (s *Store) Apply(event events.Event, cursor events.LogCursor) {
	if event.SessionID == "" || cursor.Offset == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sources[event.SessionID] = cursor
	if !s.initialized[event.SessionID] {
		if len(s.subscribers) == 0 || event.Type != events.SessionCreated {
			return
		}
		s.states[event.SessionID] = Empty(event.SessionID)
		s.initialized[event.SessionID] = true
	}
	previous := s.states[event.SessionID]
	if previous.Cursor.Generation == cursor.Generation && previous.Cursor.Offset >= cursor.Offset {
		return
	}
	next, _, err := Next(previous, Record{Cursor: fromLogCursor(cursor), Event: event})
	if err != nil {
		s.stale[event.SessionID] = err.Error()
		return
	}
	next.Stale, next.StaleReason = false, ""
	delete(s.stale, event.SessionID)
	patch := diff(previous, next)
	s.states[event.SessionID] = next
	s.broadcastLocked(patch)
}

func (s *Store) MarkStale(event events.Event, err error) {
	if event.SessionID == "" || err == nil {
		return
	}
	s.mu.Lock()
	s.stale[event.SessionID] = fmt.Sprintf("event %s was not appended: %v", event.Type, err)
	for id, ch := range s.subscribers {
		delete(s.subscribers, id)
		close(ch)
	}
	s.mu.Unlock()
}

func (s *Store) Snapshot(sources map[string]events.LogCursor) (map[string]Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked(sources)
}

func (s *Store) Delete(sessionID string) {
	s.mu.Lock()
	delete(s.states, sessionID)
	delete(s.sources, sessionID)
	delete(s.initialized, sessionID)
	delete(s.stale, sessionID)
	s.cache.Clear()
	s.mu.Unlock()
}

// SubscribeSnapshot establishes one atomic cut: the subscriber is registered while
// the captured durable offsets are projected. Apply blocks until the snapshot is ready,
// so every later patch is strictly after its cursor.
func (s *Store) SubscribeSnapshot(sources map[string]events.LogCursor) (map[string]Snapshot, <-chan Patch, func(), error) {
	s.mu.Lock()
	id := s.next
	s.next++
	ch := make(chan Patch, 128)
	s.subscribers[id] = ch
	values, err := s.snapshotLocked(sources)
	if err != nil {
		delete(s.subscribers, id)
		close(ch)
		s.mu.Unlock()
		return nil, nil, func() {}, err
	}
	s.mu.Unlock()
	var once sync.Once
	return values, ch, func() {
		once.Do(func() {
			s.mu.Lock()
			if current, ok := s.subscribers[id]; ok {
				delete(s.subscribers, id)
				close(current)
			}
			s.mu.Unlock()
		})
	}, nil
}

func (s *Store) snapshotLocked(sources map[string]events.LogCursor) (map[string]Snapshot, error) {
	for id, source := range sources {
		known := s.sources[id]
		if known.Generation != source.Generation || known.Offset < source.Offset {
			s.sources[id] = source
		}
	}
	result := map[string]Snapshot{}
	ids := make([]string, 0, len(sources))
	for id := range sources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		source := s.sources[id]
		state := s.states[id]
		if !s.initialized[id] || state.Cursor.Generation != source.Generation || state.Cursor.Offset != source.Offset {
			projected, err := s.cache.ProjectFile(source.Path, source.Offset)
			if err != nil {
				return nil, fmt.Errorf("project session %s: %w", id, err)
			}
			state = projected
			s.states[id] = state
			s.initialized[id] = true
		}
		if reason := s.stale[id]; reason != "" {
			state.Stale, state.StaleReason = true, reason
			s.states[id] = state
		}
		result[id] = state
	}
	return cloneSnapshots(result), nil
}

func (s *Store) broadcastLocked(patch Patch) {
	for id, ch := range s.subscribers {
		select {
		case ch <- patch:
		default:
			// A dropped patch would make the client silently wrong. Closing forces
			// EventSource to reconnect and receive a fresh atomic snapshot.
			delete(s.subscribers, id)
			close(ch)
		}
	}
}

func fromLogCursor(value events.LogCursor) Cursor {
	return Cursor{Generation: value.Generation, Offset: value.Offset}
}

func cloneSnapshots(values map[string]Snapshot) map[string]Snapshot {
	result := make(map[string]Snapshot, len(values))
	for id, value := range values {
		result[id] = copySnapshot(value)
	}
	return result
}

func copySnapshot(value Snapshot) Snapshot {
	encoded, _ := json.Marshal(value)
	var result Snapshot
	_ = json.Unmarshal(encoded, &result)
	return result
}
