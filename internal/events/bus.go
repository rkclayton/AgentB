package events

import "sync"

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
	if event.SessionID == "" {
		b.global = appendBounded(b.global, event, 200)
	} else {
		b.sessionRing[event.SessionID] = appendBounded(b.sessionRing[event.SessionID], event, 500)
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
