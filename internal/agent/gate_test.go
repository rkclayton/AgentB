package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
)

func TestGateRequiredMatrix(t *testing.T) {
	tests := []struct {
		mode string
		want map[string]bool
	}{
		{mode: config.ApprovalModeOff, want: map[string]bool{}},
		{mode: config.ApprovalModeBoundaryOnly, want: map[string]bool{}},
		{mode: config.ApprovalModeMutating, want: map[string]bool{"write_file": true, "edit_file": true, "shell": true, "run_script": true}},
		{mode: config.ApprovalModeAll, want: map[string]bool{"read_file": true, "write_file": true, "edit_file": true, "shell": true, "run_script": true, "fetch_url": true}},
	}
	tools := []string{"read_file", "write_file", "edit_file", "shell", "run_script", "fetch_url"}
	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			cfg := config.Defaults(t.TempDir())
			cfg.Approval.Mode = test.mode
			gate := NewGate(nil, func() config.Config { return cfg })
			for _, name := range tools {
				if got := gate.required(name); got != test.want[name] {
					t.Errorf("required(%q)=%v, want %v", name, got, test.want[name])
				}
			}
		})
	}
}

func TestCanceledApprovalPublishesDismissedDecision(t *testing.T) {
	bus := events.NewBus()
	eventCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	cfg := config.Defaults(t.TempDir())
	gate := NewGate(bus, func() config.Config { return cfg })
	s := &session.Session{ID: "session", Run: session.RunState{Status: "running"}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := gate.WaitBoundaryDecision(ctx, s, "run", "call", "read_file.operator_override", map[string]any{"path": "outside.txt"})
		done <- err
	}()
	if event := <-eventCh; event.Type != events.ApprovalRequired {
		t.Fatalf("first event=%s", event.Type)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled wait returned nil")
	}
	decided := <-eventCh
	if decided.Type != events.ApprovalDecided || decided.Data.(map[string]any)["decision"] != "dismissed" {
		t.Fatalf("decision=%#v", decided)
	}
}

func TestNewApprovalSupersedesExistingSessionDecision(t *testing.T) {
	bus := events.NewBus()
	eventCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	cfg := config.Defaults(t.TempDir())
	gate := NewGate(bus, func() config.Config { return cfg })
	s := &session.Session{ID: "session", Run: session.RunState{Status: "running"}}
	var waits sync.WaitGroup
	waits.Add(2)
	type result struct {
		decision string
		err      error
	}
	results := make(chan result, 2)
	go func() {
		defer waits.Done()
		decision, err := gate.WaitBoundaryDecision(context.Background(), s, "run", "first", "read_file.operator_override", map[string]any{})
		results <- result{decision: decision, err: err}
	}()
	if event := <-eventCh; event.Type != events.ApprovalRequired {
		t.Fatalf("first event=%s", event.Type)
	}
	go func() {
		defer waits.Done()
		decision, err := gate.WaitBoundaryDecision(context.Background(), s, "run", "second", "shell.operator_override", map[string]any{})
		results <- result{decision: decision, err: err}
	}()
	superseded, required := <-eventCh, <-eventCh
	if superseded.Type != events.ApprovalDecided || superseded.Data.(map[string]any)["decision"] != "superseded" || required.Type != events.ApprovalRequired || required.Data.(map[string]any)["call_id"] != "second" {
		t.Fatalf("events=%#v %#v", superseded, required)
	}
	if err := gate.Decide(s.ID, "second", "deny"); err != nil {
		t.Fatal(err)
	}
	<-eventCh
	waits.Wait()
	close(results)
	values := map[string]result{}
	for value := range results {
		values[value.decision] = value
	}
	if values["superseded"].err == nil || values["deny"].err != nil {
		t.Fatalf("decisions=%v", values)
	}
}

func TestFileBoundaryAcceptsRunAndSessionButNotOperatorMode(t *testing.T) {
	for _, decision := range []string{"run", "session"} {
		t.Run(decision, func(t *testing.T) {
			bus := events.NewBus()
			eventCh, unsubscribe := bus.Subscribe()
			defer unsubscribe()
			cfg := config.Defaults(t.TempDir())
			gate := NewGate(bus, func() config.Config { return cfg })
			s := &session.Session{ID: "session", Run: session.RunState{Status: "running"}}
			done := make(chan string, 1)
			go func() {
				value, _ := gate.WaitBoundaryDecision(context.Background(), s, "run", "call", "write_file.operator_override", map[string]any{})
				done <- value
			}()
			<-eventCh
			if err := gate.Decide(s.ID, "call", "operator_mode"); err == nil {
				t.Fatal("file approval accepted operator mode")
			}
			if err := gate.Decide(s.ID, "call", decision); err != nil {
				t.Fatal(err)
			}
			if got := <-done; got != decision {
				t.Fatalf("decision=%q", got)
			}
		})
	}
}

func TestPolicyApprovalEventIsNotBoundaryEscape(t *testing.T) {
	bus := events.NewBus()
	eventCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	cfg := config.Defaults(t.TempDir())
	cfg.Approval.Mode = config.ApprovalModeMutating
	gate := NewGate(bus, func() config.Config { return cfg })
	s := &session.Session{ID: "session", Run: session.RunState{Status: "running"}}
	done := make(chan bool, 1)
	go func() {
		approved, _ := gate.Wait(context.Background(), s, "run", "call", "write_file", map[string]any{"path": "file.txt"})
		done <- approved
	}()
	select {
	case event := <-eventCh:
		data, ok := event.Data.(map[string]any)
		if event.Type != events.ApprovalRequired || !ok || data["boundary_escape"] != false {
			t.Fatalf("policy event=%#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for policy approval")
	}
	if err := gate.Decide(s.ID, "call", "approve"); err != nil {
		t.Fatal(err)
	}
	select {
	case approved := <-done:
		if !approved {
			t.Fatal("policy approval did not approve")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for policy decision")
	}
}

func TestRunAndOperatorModeDecisionsAreShellOnly(t *testing.T) {
	bus := events.NewBus()
	eventsCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	cfg := config.Defaults(t.TempDir())
	cfg.Approval.Mode = config.ApprovalModeAll
	gate := NewGate(bus, func() config.Config { return cfg })
	s := &session.Session{ID: "session", Run: session.RunState{Status: "running"}}
	done := make(chan bool, 1)
	go func() {
		approved, _ := gate.Wait(context.Background(), s, "run", "call", "write_file", map[string]any{"path": "file.txt"})
		done <- approved
	}()
	select {
	case <-eventsCh:
	case <-time.After(2 * time.Second):
		t.Fatal("approval not published")
	}
	called := false
	if err := gate.DecideWith(s.ID, "call", "operator_mode", func() { called = true }); err == nil {
		t.Fatal("operator_mode accepted for non-shell approval")
	}
	if called {
		t.Fatal("operator callback ran for non-shell approval")
	}
	if err := gate.Decide(s.ID, "call", "run"); err == nil {
		t.Fatal("run accepted for non-shell approval")
	}
	if err := gate.Decide(s.ID, "call", "deny"); err != nil {
		t.Fatal(err)
	}
	<-done
}
