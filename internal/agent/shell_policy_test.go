package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
	"harness/internal/tools"
)

type shellPolicyTool struct {
	normalCalls   int
	overrideCalls int
	offerOverride bool
}

func (*shellPolicyTool) Name() string           { return "shell" }
func (*shellPolicyTool) Description() string    { return "test shell" }
func (*shellPolicyTool) Schema() map[string]any { return map[string]any{} }
func (t *shellPolicyTool) Call(context.Context, *session.Session, map[string]any) (string, error) {
	return "unused", nil
}
func (t *shellPolicyTool) CallDetailed(context.Context, *session.Session, map[string]any) tools.CallDetail {
	t.normalCalls++
	if t.offerOverride {
		return tools.CallDetail{Content: "exit=1\nAccess is denied.", OperatorOverrideReason: "service account was denied permission"}
	}
	return tools.CallDetail{Content: "exit=0\nservice-ok"}
}
func (t *shellPolicyTool) CallAsOperator(context.Context, *session.Session, map[string]any) (string, error) {
	t.overrideCalls++
	return "exit=0\noperator-ok", nil
}

func shellPolicyRunner(t *testing.T, cfg config.Config, tool *shellPolicyTool) (*Runner, *session.Session, *events.Bus) {
	t.Helper()
	bus := events.NewBus()
	runner := &Runner{bus: bus, tools: tools.New(tool), cfg: func() config.Config { return cfg }}
	runner.gate = NewGate(bus, runner.cfg)
	s := &session.Session{
		ID:           "session",
		Workspace:    t.TempDir(),
		Run:          session.RunState{Status: "running"},
		ToolsEnabled: map[string]bool{"shell": true},
	}
	return runner, s, bus
}

func nextApprovalEvent(t *testing.T, eventCh <-chan events.Event) events.Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-eventCh:
			if event.Type == events.ApprovalRequired {
				return event
			}
		case <-deadline:
			t.Fatal("timed out waiting for approval")
		}
	}
}

func TestShellApprovalMatrixByServiceAccountPosture(t *testing.T) {
	modes := []string{
		config.ApprovalModeBoundaryOnly,
		config.ApprovalModeOff,
		config.ApprovalModeMutating,
		config.ApprovalModeAll,
	}
	for _, serviceEnabled := range []bool{false, true} {
		for _, mode := range modes {
			name := "service-off/" + mode
			if serviceEnabled {
				name = "service-on/" + mode
			}
			t.Run(name, func(t *testing.T) {
				cfg := config.Defaults(t.TempDir())
				cfg.Approval.Mode = mode
				cfg.Shell.ServiceAccount.Enabled = serviceEnabled
				tool := &shellPolicyTool{}
				runner, s, bus := shellPolicyRunner(t, cfg, tool)
				eventCh, unsubscribe := bus.Subscribe()
				defer unsubscribe()
				done := make(chan tools.CallOutcome, 1)
				args := map[string]any{"command": "Write-Output service-ok"}
				go func() { done <- runner.executeTool(context.Background(), s, "run", "call", "shell", args) }()

				wantPrompt := serviceEnabled || mode == config.ApprovalModeMutating || mode == config.ApprovalModeAll
				if !wantPrompt {
					select {
					case event := <-eventCh:
						t.Fatalf("unexpected approval event: %#v", event)
					case outcome := <-done:
						if !outcome.OK || outcome.OperatorContext || tool.normalCalls != 1 || tool.overrideCalls != 0 {
							t.Fatalf("silent outcome=%+v normal=%d operator=%d", outcome, tool.normalCalls, tool.overrideCalls)
						}
					case <-time.After(2 * time.Second):
						t.Fatal("silent shell call did not complete")
					}
					return
				}

				event := nextApprovalEvent(t, eventCh)
				data, ok := event.Data.(map[string]any)
				if !ok || data["boundary_escape"] != false || data["call_id"] != "call" || data["name"] != "shell" {
					t.Fatalf("shell policy event=%#v", event)
				}
				eventArgs, ok := data["args"].(map[string]any)
				if !ok || eventArgs["command"] != args["command"] {
					t.Fatalf("shell policy args=%#v", data["args"])
				}
				if tool.normalCalls != 0 || tool.overrideCalls != 0 {
					t.Fatalf("shell executed before approval: normal=%d operator=%d", tool.normalCalls, tool.overrideCalls)
				}
				if err := runner.gate.Decide(s.ID, "call", "approve"); err != nil {
					t.Fatal(err)
				}
				select {
				case outcome := <-done:
					if !outcome.OK || outcome.OperatorContext || tool.normalCalls != 1 || tool.overrideCalls != 0 {
						t.Fatalf("approved outcome=%+v normal=%d operator=%d", outcome, tool.normalCalls, tool.overrideCalls)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("approved shell call did not complete")
				}
			})
		}
	}
}

func TestDeniedServiceShellPolicyExecutesNothing(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	cfg.Approval.Mode = config.ApprovalModeBoundaryOnly
	cfg.Shell.ServiceAccount.Enabled = true
	tool := &shellPolicyTool{}
	runner, s, bus := shellPolicyRunner(t, cfg, tool)
	eventCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	done := make(chan tools.CallOutcome, 1)
	go func() {
		done <- runner.executeTool(context.Background(), s, "run", "call", "shell", map[string]any{"command": "Write-Output must-not-run"})
	}()
	event := nextApprovalEvent(t, eventCh)
	data := event.Data.(map[string]any)
	if data["boundary_escape"] != false {
		t.Fatalf("shell policy was classified as boundary escape: %#v", data)
	}
	if err := runner.gate.Decide(s.ID, "call", "deny"); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome := <-done:
		if outcome.OK || !strings.Contains(outcome.Content, "denied by user") || outcome.OperatorContext {
			t.Fatalf("denied outcome=%+v", outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("denied shell policy did not complete")
	}
	if tool.normalCalls != 0 || tool.overrideCalls != 0 {
		t.Fatalf("denied shell executed: normal=%d operator=%d", tool.normalCalls, tool.overrideCalls)
	}
}

func TestOrdinaryToolExecutionReportsStartAndCompletionActivity(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	cfg.Approval.Mode = config.ApprovalModeBoundaryOnly
	tool := &shellPolicyTool{}
	runner, s, _ := shellPolicyRunner(t, cfg, tool)
	phases := []string{}
	runner.SetToolActivity(func(phase string) { phases = append(phases, phase) })

	outcome := runner.executeTool(context.Background(), s, "run", "call", "shell", map[string]any{"command": "Write-Output service-ok"})
	if !outcome.OK || outcome.OperatorContext {
		t.Fatalf("outcome=%+v", outcome)
	}
	if strings.Join(phases, ",") != "started,completed" {
		t.Fatalf("activity phases=%#v", phases)
	}
}

func TestApprovedServiceShellDenialStillRequiresOperatorEscape(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	cfg.Approval.Mode = config.ApprovalModeBoundaryOnly
	cfg.Shell.ServiceAccount.Enabled = true
	tool := &shellPolicyTool{offerOverride: true}
	runner, s, bus := shellPolicyRunner(t, cfg, tool)
	phases := []string{}
	runner.SetToolActivity(func(phase string) { phases = append(phases, phase) })
	eventCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	done := make(chan tools.CallOutcome, 1)
	go func() {
		done <- runner.executeTool(context.Background(), s, "run", "call", "shell", map[string]any{"command": "Write-Output denied"})
	}()

	policy := nextApprovalEvent(t, eventCh).Data.(map[string]any)
	if policy["boundary_escape"] != false || policy["call_id"] != "call" || policy["name"] != "shell" {
		t.Fatalf("policy event=%#v", policy)
	}
	if err := runner.gate.Decide(s.ID, "call", "approve"); err != nil {
		t.Fatal(err)
	}
	escape := nextApprovalEvent(t, eventCh).Data.(map[string]any)
	if escape["boundary_escape"] != true || escape["call_id"] != "call:operator" || escape["name"] != "shell.operator_override" {
		t.Fatalf("escape event=%#v", escape)
	}
	if tool.normalCalls != 1 || tool.overrideCalls != 0 {
		t.Fatalf("before escape approval normal=%d operator=%d", tool.normalCalls, tool.overrideCalls)
	}
	if err := runner.gate.Decide(s.ID, "call:operator", "approve"); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome := <-done:
		if !outcome.OK || !outcome.OperatorContext || tool.normalCalls != 1 || tool.overrideCalls != 1 {
			t.Fatalf("escaped outcome=%+v normal=%d operator=%d", outcome, tool.normalCalls, tool.overrideCalls)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("operator escape did not complete")
	}
	if strings.Join(phases, ",") != "started,completed" {
		t.Fatalf("one-shot operator escape changed activity phases=%#v", phases)
	}
}
