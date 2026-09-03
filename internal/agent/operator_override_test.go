package agent

import (
	"context"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
	"harness/internal/tools"
)

type overrideTestTool struct {
	normalCalls   int
	overrideCalls int
}

func (*overrideTestTool) Name() string           { return "shell" }
func (*overrideTestTool) Description() string    { return "test" }
func (*overrideTestTool) Schema() map[string]any { return map[string]any{} }
func (*overrideTestTool) Call(context.Context, *session.Session, map[string]any) (string, error) {
	return "unused", nil
}
func (t *overrideTestTool) CallDetailed(context.Context, *session.Session, map[string]any) (string, error, bool) {
	t.normalCalls++
	return "exit=1\nAccess to the path is denied.", nil, true
}
func (t *overrideTestTool) CallAsOperator(context.Context, *session.Session, map[string]any) (string, error) {
	t.overrideCalls++
	return "exit=0\noperator-ok", nil
}

func TestOperatorOverrideRequiresApprovalEvenWhenApprovalModeOff(t *testing.T) {
	bus := events.NewBus()
	eventCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	cfg := config.Defaults(t.TempDir())
	cfg.Approval.Mode = "off"
	tool := &overrideTestTool{}
	runner := &Runner{bus: bus, tools: tools.New(tool), cfg: func() config.Config { return cfg }}
	runner.gate = NewGate(bus, runner.cfg)
	s := &session.Session{ID: "session", Workspace: t.TempDir(), Run: session.RunState{Status: "running"}, ToolsEnabled: map[string]bool{"shell": true}}
	type result struct {
		content string
		ok      bool
	}
	done := make(chan result, 1)
	go func() {
		content, ok := runner.executeTool(context.Background(), s, "run", "call", "shell", map[string]any{"command": "Set-Content protected.txt value"})
		done <- result{content: content, ok: ok}
	}()

	var required events.Event
	select {
	case required = <-eventCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for operator approval")
	}
	if required.Type != events.ApprovalRequired {
		t.Fatalf("event type=%q, want %q", required.Type, events.ApprovalRequired)
	}
	data, ok := required.Data.(map[string]any)
	if !ok || data["call_id"] != "call:operator" || data["name"] != "shell.operator_override" {
		t.Fatalf("approval data=%#v", required.Data)
	}
	args, ok := data["args"].(map[string]any)
	if !ok || args["command"] != "Set-Content protected.txt value" || args["scope"] != "rerun this exact command once" {
		t.Fatalf("approval args=%#v", data["args"])
	}
	if status := s.Snapshot(nil).Run.Status; status != "paused" {
		t.Fatalf("run status=%q, want paused", status)
	}
	if err := runner.gate.Decide(s.ID, "call:operator", "approve"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if !got.ok || got.content != "exit=1\nAccess to the path is denied.\n\noperator-identity override approved; exact command rerun once:\nexit=0\noperator-ok" {
			t.Fatalf("result=%#v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approved retry")
	}
	if tool.normalCalls != 1 || tool.overrideCalls != 1 {
		t.Fatalf("normal calls=%d override calls=%d", tool.normalCalls, tool.overrideCalls)
	}
}

func TestOperatorOverrideDenialDoesNotRetry(t *testing.T) {
	bus := events.NewBus()
	eventCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	cfg := config.Defaults(t.TempDir())
	cfg.Approval.Mode = "off"
	tool := &overrideTestTool{}
	runner := &Runner{bus: bus, tools: tools.New(tool), cfg: func() config.Config { return cfg }}
	runner.gate = NewGate(bus, runner.cfg)
	s := &session.Session{ID: "session", Workspace: t.TempDir(), Run: session.RunState{Status: "running"}, ToolsEnabled: map[string]bool{"shell": true}}
	done := make(chan struct{}, 1)
	go func() {
		_, _ = runner.executeTool(context.Background(), s, "run", "call", "shell", map[string]any{"command": "Set-Content protected.txt value"})
		done <- struct{}{}
	}()
	select {
	case <-eventCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for operator approval")
	}
	if err := runner.gate.Decide(s.ID, "call:operator", "deny"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for denied retry")
	}
	if tool.overrideCalls != 0 {
		t.Fatalf("denied override ran %d times", tool.overrideCalls)
	}
}
