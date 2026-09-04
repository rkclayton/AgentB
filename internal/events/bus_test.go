package events

import "testing"

func TestRecentOmitsDiagnosticPayloadsButSinkReceivesThem(t *testing.T) {
	bus := NewBus()
	var logged Event
	bus.SetSink(func(event Event) { logged = event })

	event := New(ModelResponse, "main", "r1", map[string]any{"content": "done"})
	event.Body = map[string]any{"messages": []string{"request"}}
	event.Raw = "raw model stream"
	published := bus.Publish(event)

	if published.Body == nil || published.Raw == nil {
		t.Fatal("Publish removed diagnostic payloads from its result")
	}
	if logged.Body == nil || logged.Raw == nil {
		t.Fatal("log sink did not receive diagnostic payloads")
	}
	recent := bus.Recent("main")
	if len(recent) != 1 {
		t.Fatalf("Recent returned %d events, want 1", len(recent))
	}
	if recent[0].Body != nil || recent[0].Raw != nil {
		t.Fatalf("Recent exposed log-only payloads: body=%v raw=%v", recent[0].Body, recent[0].Raw)
	}
}
