package events

import (
	"fmt"
	"sync"
)

type Bus struct {
	mu          sync.Mutex
	seq         int64
	subscribers map[int]chan Event
	next        int
	sessionRing map[string][]Event
	global      []Event
	sink        func(Event)
}

func NewBus() *Bus                      { return &Bus{subscribers: map[int]chan Event{}, sessionRing: map[string][]Event{}} }
func (b *Bus) SetSink(sink func(Event)) { b.mu.Lock(); b.sink = sink; b.mu.Unlock() }
func (b *Bus) Publish(event Event) Event {
	b.mu.Lock()
	b.seq++
	event.Seq = b.seq
	if event.TS == "" {
		event = New(event.Type, event.SessionID, event.RunID, event.Data)
		event.Seq = b.seq
	}
	// Body and Raw are diagnostic log payloads. Live SSE already strips them,
	// and retaining them in the recent-event ring makes every new UI snapshot carry
	// a duplicate model request or complete raw token stream.
	recentEvent := event
	recentEvent.Body = nil
	recentEvent.Raw = nil
	if event.SessionID == "" {
		b.global = appendBounded(b.global, recentEvent, 200)
	} else {
		b.sessionRing[event.SessionID] = appendRecent(b.sessionRing[event.SessionID], recentEvent, 500)
	}
	subscribers := make([]chan Event, 0, len(b.subscribers))
	for _, ch := range b.subscribers {
		subscribers = append(subscribers, ch)
	}
	sink := b.sink
	b.mu.Unlock()
	if sink != nil {
		sink(event)
	}
	for _, ch := range subscribers {
		select {
		case ch <- event:
		default:
		}
	}
	return event
}
func (b *Bus) Subscribe() (chan Event, func()) {
	b.mu.Lock()
	id := b.next
	b.next++
	ch := make(chan Event, 128)
	b.subscribers[id] = ch
	b.mu.Unlock()
	var once sync.Once
	return ch, func() { once.Do(func() { b.mu.Lock(); delete(b.subscribers, id); b.mu.Unlock() }) }
}
func (b *Bus) Recent(sessionID string) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	source := b.global
	if sessionID != "" {
		source = b.sessionRing[sessionID]
	}
	return append([]Event(nil), source...)
}
func appendBounded(values []Event, value Event, limit int) []Event {
	values = append(values, value)
	if len(values) > limit {
		return append([]Event(nil), values[len(values)-limit:]...)
	}
	return values
}

// appendRecent retains the current model stream until its response arrives, then
// removes its raw progress/delta events. Completed token streams are diagnostic log
// detail; they must not displace turns, tool results, or exceptional run events from
// the live snapshot.
func appendRecent(values []Event, value Event, limit int) []Event {
	values = append(values, value)
	switch value.Type {
	case ModelDelta, ModelProgress:
		return values
	case ModelResponse:
		values = discardStream(values, func(event Event) bool {
			return sameTurn(event, value)
		})
	case RunStopped:
		values = discardStream(values, func(event Event) bool {
			return event.RunID == value.RunID
		})
	}
	// Stage-exit and other operational events can arrive between the final delta
	// and ModelResponse. Do not bound the ring while any live stream remains or
	// those events can evict the history the response is meant to preserve.
	if hasStream(values) {
		return values
	}
	if len(values) > limit {
		return append([]Event(nil), values[len(values)-limit:]...)
	}
	return values
}

func hasStream(values []Event) bool {
	for _, event := range values {
		if event.Type == ModelDelta || event.Type == ModelProgress {
			return true
		}
	}
	return false
}

func discardStream(values []Event, matches func(Event) bool) []Event {
	out := values[:0]
	for _, event := range values {
		if (event.Type == ModelDelta || event.Type == ModelProgress) && matches(event) {
			continue
		}
		out = append(out, event)
	}
	return out
}

func sameTurn(left, right Event) bool {
	if left.RunID != right.RunID {
		return false
	}
	return eventTurn(left) == eventTurn(right)
}

func eventTurn(event Event) string {
	if data, ok := event.Data.(map[string]any); ok {
		return fmt.Sprint(data["turn"])
	}
	return ""
}
