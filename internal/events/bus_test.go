package events

import (
	"fmt"
	"testing"
)

func TestSinkReceivesDiagnosticPayloads(t *testing.T) {
	bus := NewBus()
	var logged Event
	bus.SetSink(func(event Event) error { logged = event; return nil })
	event := New(ModelResponse, "main", "r1", map[string]any{"content": "done"})
	event.Body, event.Raw = map[string]any{"messages": []string{"request"}}, "raw model stream"
	published := bus.Publish(event)
	if published.Body == nil || published.Raw == nil || logged.Body == nil || logged.Raw == nil {
		t.Fatal("diagnostic payload was removed before persistence")
	}
}

func TestSinkFailureBecomesOperationalErrorAndMarksProjectionStale(t *testing.T) {
	bus := NewBus()
	seen := make(chan Event, 4)
	stream, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	bus.SetDurableSink(func(Event) (LogCursor, error) { return LogCursor{}, fmt.Errorf("disk full") }, nil, func(event Event, err error) { seen <- event })
	bus.Publish(New(ToolResult, "main", "run", map[string]any{"name": "shell"}))
	if stale := <-seen; stale.Type != ToolResult {
		t.Fatalf("stale event=%s", stale.Type)
	}
	first, second := <-stream, <-stream
	if first.Type != ToolResult || second.Type != Error {
		t.Fatalf("stream=%s,%s", first.Type, second.Type)
	}
	data := second.Data.(map[string]any)
	if data["where"] != "event_log" || data["lost_event_type"] != ToolResult {
		t.Fatalf("error data=%#v", data)
	}
}

func TestSubscriberOverflowClosesForResync(t *testing.T) {
	bus := NewBus()
	stream, _ := bus.Subscribe()
	for index := 0; index < 130; index++ {
		bus.Publish(New(ModelDelta, "main", "r1", map[string]any{"turn": 1, "text": "x"}))
	}
	for range 128 {
		<-stream
	}
	if _, ok := <-stream; ok {
		t.Fatal("overflowed subscriber remained open")
	}
}

func TestSubscriberCountTracksSubscribeAndUnsubscribe(t *testing.T) {
	bus := NewBus()
	if got := bus.SubscriberCount(); got != 0 {
		t.Fatalf("initial subscriber count = %d", got)
	}
	_, unsubscribeFirst := bus.Subscribe()
	_, unsubscribeSecond := bus.Subscribe()
	if got := bus.SubscriberCount(); got != 2 {
		t.Fatalf("subscribed count = %d", got)
	}
	unsubscribeFirst()
	if got := bus.SubscriberCount(); got != 1 {
		t.Fatalf("after first unsubscribe = %d", got)
	}
	unsubscribeFirst()
	unsubscribeSecond()
	if got := bus.SubscriberCount(); got != 0 {
		t.Fatalf("final subscriber count = %d", got)
	}
}
