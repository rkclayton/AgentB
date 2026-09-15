package progress

import (
	"testing"
	"time"

	"harness/internal/events"
)

func TestNormalizeResultStripsVolatileValues(t *testing.T) {
	left := `2026-09-15T12:01:02.123Z PID 123 elapsed=421ms C:\Users\Randy\AppData\Local\Temp\one\out.txt offset=101 connection refused`
	right := `2026-09-16T13:02:03.999Z PID 987 elapsed=922ms C:\Users\Randy\AppData\Local\Temp\two\out.txt offset=999 connection refused`
	if got, want := ResultHash(left), ResultHash(right); got != want {
		t.Fatalf("volatile values did not normalize: %s != %s\n%s\n%s", got, want, NormalizeResult(left), NormalizeResult(right))
	}
}

func TestManagerWritesSevenShadowRecordsWithoutStopping(t *testing.T) {
	bus := events.NewBus()
	manager := New(bus)
	manager.Start()
	defer manager.Close()
	seen, stop := bus.Subscribe()
	defer stop()
	armed := ArmedSet(false)
	bus.Publish(events.New(events.RunStarted, "s1", "r1", map[string]any{"armed_detectors": armed}))
	bus.Publish(events.New(events.ModelRequest, "s1", "r1", map[string]any{"turn": 1, "params": map[string]any{"reasoning": map[string]any{"enabled": false}}}))
	bus.Publish(events.New(events.ToolCallEvent, "s1", "r1", map[string]any{"turn": 1, "name": "shell", "args": map[string]any{"command": "go test ./..."}}))
	bus.Publish(events.New(events.ToolResult, "s1", "r1", map[string]any{"turn": 1, "name": "shell", "ok": true, "preview": "ok"}))
	bus.Publish(events.New(events.RunStopped, "s1", "r1", map[string]any{"reason": "done", "turns": 1}))
	deadline := time.After(time.Second)
	records := map[string]Record{}
	for len(records) < 7 {
		select {
		case event := <-seen:
			if event.Type != events.ProgressShadow {
				continue
			}
			value, ok := event.Data.(Record)
			if !ok {
				t.Fatalf("shadow data type %T", event.Data)
			}
			records[value.Detector] = value
		case <-deadline:
			t.Fatalf("got %d shadow records", len(records))
		}
	}
	if records[ModelSaysStuck].Available {
		t.Fatal("reasoning-off signal must be absent")
	}
	if records[AuxProgress].Armed || records[AuxProgress].Available {
		t.Fatal("aux signal must be absent without an aux profile")
	}
	for name, record := range records {
		if record.WouldFire {
			t.Fatalf("short productive run fired %s", name)
		}
	}
}

func TestEvaluateGuessedThresholds(t *testing.T) {
	state := &runState{armed: map[string]bool{}, novelTurns: map[int]bool{}, turnTools: map[int]map[string]bool{}, durations: map[int]float64{}, reasoningKnown: true, reasoningOn: true}
	for _, name := range DetectorNames {
		state.armed[name] = true
	}
	state.maxTurn = 5
	state.results = []string{"connection refused", "connection refused", "connection refused", "connection refused", "connection refused"}
	state.timeouts = []bool{true, true, true, false, false}
	state.errors = []bool{true, true, true, true, true, true, false, false, false, false}
	state.reasoning = "i'm stuck. i am stuck."
	state.auxKnown, state.auxStuck = true, true
	records := Evaluate(state)
	for _, record := range records {
		if record.Detector == BaselineDeviation {
			continue
		}
		if !record.WouldFire {
			t.Errorf("expected %s to fire", record.Detector)
		}
	}
}

func TestEvaluateEventsRetainsTransientFirstFire(t *testing.T) {
	stream := []events.Event{}
	for turn := 1; turn <= 8; turn++ {
		stream = append(stream, events.New(events.ToolResult, "s1", "r1", map[string]any{"turn": turn, "ok": true, "preview": "same result"}))
	}
	stream = append(stream, events.New(events.ToolResult, "s1", "r1", map[string]any{"turn": 9, "ok": true, "preview": "different 1"}))
	stream = append(stream, events.New(events.ToolResult, "s1", "r1", map[string]any{"turn": 10, "ok": true, "preview": "different 2"}))
	for _, record := range EvaluateEvents(stream, ArmedSet(false)) {
		if record.Detector != ResultRepetition {
			continue
		}
		if !record.WouldFire || intValue(record.Values["first_fire_turn"]) != 5 {
			t.Fatalf("result repetition=%+v", record)
		}
		return
	}
	t.Fatal("result repetition record missing")
}
