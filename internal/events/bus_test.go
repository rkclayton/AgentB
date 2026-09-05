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

func TestRecentRetainsActiveStreamThenDiscardsCompletedDeltas(t *testing.T) {
	bus := NewBus()
	bus.Publish(New(Compaction, "main", "r1", map[string]any{"before": 1000, "after": 600}))
	bus.Publish(New(ModelRequest, "main", "r1", map[string]any{"turn": 1}))
	for index := 0; index < 600; index++ {
		bus.Publish(New(ModelDelta, "main", "r1", map[string]any{"turn": 1, "kind": "reasoning", "text": "x"}))
	}
	if got := len(bus.Recent("main")); got != 602 {
		t.Fatalf("active stream length=%d, want 602", got)
	}
	bus.Publish(New(Stage, "main", "r1", map[string]any{"stage": "call_model", "state": "exit", "turn": 1}))
	if got := len(bus.Recent("main")); got != 603 {
		t.Fatalf("stage exit bounded active stream to %d events, want 603", got)
	}
	bus.Publish(New(ModelResponse, "main", "r1", map[string]any{"turn": 1}))
	recent := bus.Recent("main")
	if len(recent) != 4 {
		t.Fatalf("completed recent length=%d, want 4", len(recent))
	}
	if recent[0].Type != Compaction || recent[1].Type != ModelRequest || recent[2].Type != Stage || recent[3].Type != ModelResponse {
		t.Fatalf("completed recent types=%v, want compaction/request/stage/response", []string{recent[0].Type, recent[1].Type, recent[2].Type, recent[3].Type})
	}
}

func TestRecentKeepsCompactionAcrossManyCompletedStreams(t *testing.T) {
	bus := NewBus()
	bus.Publish(New(Compaction, "main", "r1", map[string]any{"before": 1000, "after": 600}))
	for turn := 1; turn <= 40; turn++ {
		bus.Publish(New(ModelRequest, "main", "r1", map[string]any{"turn": turn}))
		for index := 0; index < 20; index++ {
			bus.Publish(New(ModelDelta, "main", "r1", map[string]any{"turn": turn, "kind": "reasoning", "text": "x"}))
		}
		bus.Publish(New(ModelResponse, "main", "r1", map[string]any{"turn": turn}))
	}
	recent := bus.Recent("main")
	if recent[0].Type != Compaction {
		t.Fatalf("first retained event=%s, want compaction", recent[0].Type)
	}
	for _, event := range recent {
		if event.Type == ModelDelta || event.Type == ModelProgress {
			t.Fatalf("completed raw stream was retained: %s", event.Type)
		}
	}
}
