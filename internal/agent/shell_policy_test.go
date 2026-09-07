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
	workspaceinfo "harness/internal/workspace"
)

type shellPolicyTool struct {
	normalCalls   int
	overrideCalls int
	offerOverride bool
	operatorMatch *tools.OperatorCommand
	operatorArgs  map[string]any
	normalArgs    map[string]any
}

func TestApprovedRepoRunGrantDefaultSkipsShellPrompt(t *testing.T) {
	for _, split := range []bool{false, true} {
		t.Run(map[bool]string{false: "operator", true: "service"}[split], func(t *testing.T) {
			cfg := config.Defaults(t.TempDir())
			cfg.Approval.Mode = config.ApprovalModeMutating
			cfg.Shell.ServiceAccount.Enabled = split
			tool := &shellPolicyTool{}
			runner, item, bus := shellPolicyRunner(t, cfg, tool)
			item.RepoPolicy = &workspaceinfo.PolicyState{Approved: true, Policy: workspaceinfo.Policy{Version: 1, Shell: workspaceinfo.ShellPolicy{RunGrantDefaults: []string{"node --test"}}}}
			eventsCh, unsubscribe := bus.Subscribe()
			defer unsubscribe()
			outcome := runner.executeTool(context.Background(), item, "run", "call", "shell", map[string]any{"command": "node --test test/*.test.js"})
			if !outcome.OK || tool.normalCalls != 1 {
				t.Fatalf("outcome=%+v calls=%d", outcome, tool.normalCalls)
			}
			select {
			case event := <-eventsCh:
				if event.Type != events.ShellGrant {
					t.Fatalf("event=%+v", event)
				}
			case <-time.After(time.Second):
				t.Fatal("missing shell.grant")
			}
		})
	}
}

type runScriptPolicyTool struct{ calls int }

func (*runScriptPolicyTool) Name() string           { return "run_script" }
func (*runScriptPolicyTool) Description() string    { return "test script" }
func (*runScriptPolicyTool) Schema() map[string]any { return map[string]any{} }
func (t *runScriptPolicyTool) Call(context.Context, *session.Session, map[string]any) (string, error) {
	t.calls++
	return "script-ok", nil
}

func (*shellPolicyTool) Name() string           { return "shell" }
func (*shellPolicyTool) Description() string    { return "test shell" }
func (*shellPolicyTool) Schema() map[string]any { return map[string]any{} }
func (t *shellPolicyTool) Call(context.Context, *session.Session, map[string]any) (string, error) {
	return "unused", nil
}
func (t *shellPolicyTool) CallDetailed(_ context.Context, _ *session.Session, args map[string]any) tools.CallDetail {
	t.normalCalls++
	t.normalArgs = args
	if t.offerOverride {
		return tools.CallDetail{Content: "exit=1\nAccess is denied.", OperatorOverrideReason: "service account was denied permission"}
	}
	return tools.CallDetail{Content: "exit=0\nservice-ok"}
}
func (t *shellPolicyTool) CallAsOperator(_ context.Context, _ *session.Session, args map[string]any) (string, error) {
	t.overrideCalls++
	t.operatorArgs = args
	return "exit=0\noperator-ok", nil
}
func (t *shellPolicyTool) OperatorCommand(map[string]any) (tools.OperatorCommand, bool) {
	if t.operatorMatch == nil {
		return tools.OperatorCommand{}, false
	}
	return *t.operatorMatch, true
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

				wantPrompt := mode == config.ApprovalModeMutating || mode == config.ApprovalModeAll
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

func TestRunScriptRetainsSplitModeConfirmation(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	cfg.Approval.Mode = config.ApprovalModeBoundaryOnly
	cfg.Shell.ServiceAccount.Enabled = true
	bus := events.NewBus()
	tool := &runScriptPolicyTool{}
	runner := &Runner{bus: bus, tools: tools.New(tool), cfg: func() config.Config { return cfg }}
	runner.gate = NewGate(bus, runner.cfg)
	s := &session.Session{ID: "session", Workspace: t.TempDir(), Run: session.RunState{Status: "running"}, ToolsEnabled: map[string]bool{"run_script": true}}
	eventCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	done := make(chan tools.CallOutcome, 1)
	go func() {
		done <- runner.executeTool(context.Background(), s, "run", "call", "run_script", map[string]any{"source": "ok"})
	}()
	required := nextApprovalEvent(t, eventCh)
	if data := required.Data.(map[string]any); data["name"] != "run_script" || data["boundary_escape"] != false || tool.calls != 0 {
		t.Fatalf("required=%#v calls=%d", required, tool.calls)
	}
	if err := runner.gate.Decide(s.ID, "call", "approve"); err != nil {
		t.Fatal(err)
	}
	if outcome := <-done; !outcome.OK || tool.calls != 1 {
		t.Fatalf("outcome=%+v calls=%d", outcome, tool.calls)
	}
}

func TestShellSessionGrantCrossesRunsAndLapsesOnClose(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	cfg.Approval.Mode = config.ApprovalModeMutating
	cfg.Shell.ServiceAccount.Enabled = true
	tool := &shellPolicyTool{}
	runner, s, bus := shellPolicyRunner(t, cfg, tool)
	eventCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	done := make(chan tools.CallOutcome, 1)
	go func() {
		done <- runner.executeTool(context.Background(), s, "run-1", "call-1", "shell", map[string]any{"command": "Write-Output first"})
	}()
	nextApprovalEvent(t, eventCh)
	if err := runner.gate.Decide(s.ID, "call-1", "session"); err != nil {
		t.Fatal(err)
	}
	if outcome := <-done; !outcome.OK || outcome.OperatorContext {
		t.Fatalf("first=%+v", outcome)
	}
	second := runner.executeTool(context.Background(), s, "run-2", "call-2", "shell", map[string]any{"command": "Write-Output second"})
	if !second.OK || second.OperatorContext || tool.normalCalls != 2 {
		t.Fatalf("second=%+v calls=%d", second, tool.normalCalls)
	}
	foundGrant := false
	for len(eventCh) > 0 {
		event := <-eventCh
		data, _ := event.Data.(map[string]any)
		if event.Type == events.ShellGrant {
			foundGrant = data["run_id"] == "run-1" && data["scope"] == "session" && data["rule"] == shellGrantPolicy && data["identity"] == "service"
		}
	}
	if !foundGrant {
		t.Fatal("missing durable session shell.grant shape")
	}
	runner.LapseSessionGrants(s.ID)
	lapsed := <-eventCh
	lapseData, _ := lapsed.Data.(map[string]any)
	if lapsed.Type != events.ShellGrantLapsed || lapseData["run_id"] != "run-1" || lapseData["scope"] != "session" || lapseData["rule"] != shellGrantPolicy || lapseData["identity"] != "service" || lapseData["reason"] != "session closed" {
		t.Fatalf("shell lapse=%#v", lapsed)
	}
	done = make(chan tools.CallOutcome, 1)
	go func() {
		done <- runner.executeTool(context.Background(), s, "run-3", "call-3", "shell", map[string]any{"command": "Write-Output third"})
	}()
	nextApprovalEvent(t, eventCh)
	if err := runner.gate.Decide(s.ID, "call-3", "deny"); err != nil {
		t.Fatal(err)
	}
	<-done
}

func TestDeniedServiceShellPolicyExecutesNothing(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	cfg.Approval.Mode = config.ApprovalModeMutating
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

func TestShellPolicyGrantLastsForRunAndLapses(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	cfg.Approval.Mode = config.ApprovalModeMutating
	cfg.Shell.ServiceAccount.Enabled = true
	tool := &shellPolicyTool{}
	runner, s, bus := shellPolicyRunner(t, cfg, tool)
	eventCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()

	done := make(chan tools.CallOutcome, 1)
	go func() {
		done <- runner.executeTool(context.Background(), s, "run-1", "call-1", "shell", map[string]any{"command": "Write-Output first"})
	}()
	nextApprovalEvent(t, eventCh)
	if err := runner.gate.Decide(s.ID, "call-1", "run"); err != nil {
		t.Fatal(err)
	}
	if outcome := <-done; !outcome.OK || outcome.OperatorContext {
		t.Fatalf("first outcome=%+v", outcome)
	}
	second := runner.executeTool(context.Background(), s, "run-1", "call-2", "shell", map[string]any{"command": "Write-Output second"})
	if !second.OK || second.OperatorContext || tool.normalCalls != 2 {
		t.Fatalf("second outcome=%+v calls=%d", second, tool.normalCalls)
	}
	runner.lapseShellGrants(s, "run-1")
	types := []string{}
	for len(eventCh) > 0 {
		types = append(types, (<-eventCh).Type)
	}
	if !strings.Contains(strings.Join(types, ","), events.ShellGrant) || !strings.Contains(strings.Join(types, ","), events.ShellGrantLapsed) {
		t.Fatalf("events=%v", types)
	}

	done = make(chan tools.CallOutcome, 1)
	go func() {
		done <- runner.executeTool(context.Background(), s, "run-2", "call-3", "shell", map[string]any{"command": "Write-Output third"})
	}()
	nextApprovalEvent(t, eventCh)
	if tool.normalCalls != 2 {
		t.Fatal("lapsed run executed before a new decision")
	}
	if err := runner.gate.Decide(s.ID, "call-3", "deny"); err != nil {
		t.Fatal(err)
	}
	<-done
}

func TestConfiguredOperatorCommandNeverUsesServiceIdentity(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	cfg.Approval.Mode = config.ApprovalModeBoundaryOnly
	cfg.Shell.ServiceAccount.Enabled = true
	command := tools.OperatorCommand{Name: "git", Executable: `C:\Program Files\Git\cmd\git.exe`}
	tool := &shellPolicyTool{operatorMatch: &command}
	runner, s, bus := shellPolicyRunner(t, cfg, tool)
	eventCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	done := make(chan tools.CallOutcome, 1)
	go func() {
		done <- runner.executeTool(context.Background(), s, "run", "git-1", "shell", map[string]any{"command": "git status"})
	}()
	required := nextApprovalEvent(t, eventCh)
	data := required.Data.(map[string]any)
	if data["name"] != "shell.operator_command" || data["boundary_escape"] != true {
		t.Fatalf("required=%#v", required)
	}
	if tool.normalCalls != 0 || tool.overrideCalls != 0 {
		t.Fatal("operator command ran before grant")
	}
	if err := runner.gate.Decide(s.ID, "git-1", "run"); err != nil {
		t.Fatal(err)
	}
	if outcome := <-done; !outcome.OK || !outcome.OperatorContext {
		t.Fatalf("first=%+v", outcome)
	}
	if got := tool.operatorArgs["command"]; got != `git status; if ($null -ne $LASTEXITCODE) { exit $LASTEXITCODE }` {
		t.Fatalf("operator command=%q", got)
	}
	second := runner.executeTool(context.Background(), s, "run", "git-2", "shell", map[string]any{"command": "git status --short"})
	if !second.OK || !second.OperatorContext || tool.normalCalls != 0 || tool.overrideCalls != 2 {
		t.Fatalf("second=%+v normal=%d operator=%d", second, tool.normalCalls, tool.overrideCalls)
	}
}

func TestConfiguredOperatorCommandNativeExitWrapperIsPowerShellOnly(t *testing.T) {
	if !isPowerShellCommand([]string{`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`}) || !isPowerShellCommand([]string{"pwsh"}) {
		t.Fatal("PowerShell dialect was not recognized")
	}
	if isPowerShellCommand([]string{"bash", "-c"}) || isPowerShellCommand(nil) {
		t.Fatal("non-PowerShell dialect received native exit wrapper")
	}
}

func TestConfiguredOperatorCommandDenialNamesRuleAndDoesNotFallBack(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	cfg.Shell.ServiceAccount.Enabled = true
	command := tools.OperatorCommand{Name: "git", Executable: `C:\Program Files\Git\cmd\git.exe`}
	tool := &shellPolicyTool{operatorMatch: &command}
	runner, s, bus := shellPolicyRunner(t, cfg, tool)
	eventCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	done := make(chan tools.CallOutcome, 1)
	go func() {
		done <- runner.executeTool(context.Background(), s, "run", "git", "shell", map[string]any{"command": "git status"})
	}()
	nextApprovalEvent(t, eventCh)
	if err := runner.gate.Decide(s.ID, "git", "deny"); err != nil {
		t.Fatal(err)
	}
	outcome := <-done
	if outcome.OK || !strings.Contains(outcome.Content, "operator command rule: git must run as the operator") || tool.normalCalls != 0 || tool.overrideCalls != 0 {
		t.Fatalf("outcome=%+v normal=%d operator=%d", outcome, tool.normalCalls, tool.overrideCalls)
	}
}

func TestShellBoundaryGrantRerunsAndCoversLaterShellCalls(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	cfg.Approval.Mode = config.ApprovalModeBoundaryOnly
	cfg.Shell.ServiceAccount.Enabled = true
	tool := &shellPolicyTool{offerOverride: true}
	runner, s, bus := shellPolicyRunner(t, cfg, tool)
	eventCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	done := make(chan tools.CallOutcome, 1)
	go func() {
		done <- runner.executeTool(context.Background(), s, "run", "call", "shell", map[string]any{"command": "Get-Content outside.txt"})
	}()
	required := nextApprovalEvent(t, eventCh)
	data := required.Data.(map[string]any)
	if data["name"] != "shell.operator_override" || tool.normalCalls != 1 {
		t.Fatalf("required=%#v normal=%d", required, tool.normalCalls)
	}
	if err := runner.gate.Decide(s.ID, "call:operator", "run"); err != nil {
		t.Fatal(err)
	}
	if outcome := <-done; !outcome.OK || !outcome.OperatorContext {
		t.Fatalf("first=%+v", outcome)
	}
	tool.offerOverride = false
	second := runner.executeTool(context.Background(), s, "run", "later", "shell", map[string]any{"command": "Write-Output later"})
	if !second.OK || !second.OperatorContext || tool.normalCalls != 1 || tool.overrideCalls != 2 {
		t.Fatalf("second=%+v normal=%d operator=%d", second, tool.normalCalls, tool.overrideCalls)
	}
}
