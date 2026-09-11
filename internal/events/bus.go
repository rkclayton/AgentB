package events

import (
	"sync"
)

type Bus struct {
	mu          sync.Mutex
	seq         int64
	subscribers map[int]chan Event
	next        int
	sink        func(Event) error
	durableSink func(Event) (LogCursor, error)
	afterAppend func(Event, LogCursor)
	appendError func(Event, error)
}

func NewBus() *Bus                            { return &Bus{subscribers: map[int]chan Event{}} }
func (b *Bus) SetSink(sink func(Event) error) { b.mu.Lock(); b.sink = sink; b.mu.Unlock() }
func (b *Bus) SetDurableSink(sink func(Event) (LogCursor, error), appended func(Event, LogCursor), failed func(Event, error)) {
	b.mu.Lock()
	b.durableSink, b.afterAppend, b.appendError = sink, appended, failed
	b.mu.Unlock()
}
func (b *Bus) Publish(event Event) Event {
	return b.publish(event, true)
}
func (b *Bus) publish(event Event, writeSink bool) Event {
	b.mu.Lock()
	b.seq++
	event.Seq = b.seq
	if event.TS == "" {
		event = New(event.Type, event.SessionID, event.RunID, event.Data)
		event.Seq = b.seq
	}
	type subscriber struct {
		id int
		ch chan Event
	}
	subscribers := make([]subscriber, 0, len(b.subscribers))
	for id, ch := range b.subscribers {
		subscribers = append(subscribers, subscriber{id, ch})
	}
	sink, durableSink, afterAppend, appendError := b.sink, b.durableSink, b.afterAppend, b.appendError
	b.mu.Unlock()
	var sinkErr error
	var cursor LogCursor
	if writeSink && sink != nil {
		sinkErr = sink(event)
	} else if writeSink && durableSink != nil {
		cursor, sinkErr = durableSink(event)
	}
	if sinkErr == nil && event.SessionID != "" && cursor.Offset > 0 && afterAppend != nil {
		afterAppend(event, cursor)
	} else if sinkErr != nil && appendError != nil {
		appendError(event, sinkErr)
	}
	for _, subscriber := range subscribers {
		select {
		case subscriber.ch <- event:
		default:
			b.mu.Lock()
			if current, ok := b.subscribers[subscriber.id]; ok && current == subscriber.ch {
				delete(b.subscribers, subscriber.id)
				close(current)
			}
			b.mu.Unlock()
		}
	}
	if sinkErr != nil {
		b.publish(New(Error, event.SessionID, event.RunID, map[string]any{"where": "event_log", "message": sinkErr.Error(), "lost_event_type": event.Type}), false)
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

func (b *Bus) SubscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subscribers)
}
