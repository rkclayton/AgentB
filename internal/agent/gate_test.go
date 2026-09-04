package agent

import (
	"context"
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
		{mode: config.ApprovalModeMutating, want: map[string]bool{"write_file": true, "edit_file": true, "shell": true}},
		{mode: config.ApprovalModeAll, want: map[string]bool{"read_file": true, "write_file": true, "edit_file": true, "shell": true, "fetch_url": true}},
	}
	tools := []string{"read_file", "write_file", "edit_file", "shell", "fetch_url"}
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
