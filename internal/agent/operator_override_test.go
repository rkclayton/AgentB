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

type overrideTestTool struct {
	name          string
	normalCalls   int
	overrideCalls int
}

func (t *overrideTestTool) Name() string {
	if t.name == "" {
		return "shell"
	}
	return t.name
}
func (*overrideTestTool) Description() string    { return "test" }
func (*overrideTestTool) Schema() map[string]any { return map[string]any{} }
func (*overrideTestTool) Call(context.Context, *session.Session, map[string]any) (string, error) {
	return "unused", nil
}
func (t *overrideTestTool) CallDetailed(context.Context, *session.Session, map[string]any) tools.CallDetail {
	t.normalCalls++
	return tools.CallDetail{Content: "exit=1\nAccess to the path is denied.", OperatorOverrideReason: "service account was denied permission"}
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
		outcome := runner.executeTool(context.Background(), s, "run", "call", "shell", map[string]any{"command": "Set-Content protected.txt value"})
		content, ok := outcome.Content, outcome.OK
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
		if !got.ok || got.content != "operator-identity override succeeded; exact command rerun once:\nexit=0\noperator-ok" {
			t.Fatalf("result=%#v", got)
		}
		if strings.Contains(got.content, "Access to the path is denied") {
			t.Fatalf("successful override retained the superseded denial: %q", got.content)
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
	type denialResult struct {
		content string
		ok      bool
	}
	done := make(chan denialResult, 1)
	go func() {
		outcome := runner.executeTool(context.Background(), s, "run", "call", "shell", map[string]any{"command": "Set-Content protected.txt value"})
		content, ok := outcome.Content, outcome.OK
		done <- denialResult{content: content, ok: ok}
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
	case got := <-done:
		denialReported := strings.Contains(got.content, "denied by the user") || strings.Contains(got.content, "denied by user")
		if got.ok || !denialReported || !strings.Contains(got.content, "Access to the path is denied") {
			t.Fatalf("denial result=%#v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for denied retry")
	}
	if tool.overrideCalls != 0 {
		t.Fatalf("denied override ran %d times", tool.overrideCalls)
	}
}

func TestFileToolOperatorOverrideUsesPathAndExactCall(t *testing.T) {
	bus := events.NewBus()
	eventCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	cfg := config.Defaults(t.TempDir())
	cfg.Approval.Mode = "off"
	tool := &overrideTestTool{name: "read_file"}
	runner := &Runner{bus: bus, tools: tools.New(tool), cfg: func() config.Config { return cfg }}
	runner.gate = NewGate(bus, runner.cfg)
	s := &session.Session{ID: "session", Workspace: t.TempDir(), Run: session.RunState{Status: "running"}, ToolsEnabled: map[string]bool{"read_file": true}}
	type fileResult struct {
		content string
		ok      bool
	}
	done := make(chan fileResult, 1)
	go func() {
		outcome := runner.executeTool(context.Background(), s, "run", "call", "read_file", map[string]any{"path": `C:\allowed.txt`})
		content, ok := outcome.Content, outcome.OK
		done <- fileResult{content: content, ok: ok}
	}()
	required := <-eventCh
	data := required.Data.(map[string]any)
	args := data["args"].(map[string]any)
	if data["name"] != "read_file.operator_override" || args["path"] != `C:\allowed.txt` || args["scope"] != "rerun this exact tool call once" {
		t.Fatalf("approval data=%#v", data)
	}
	if err := runner.gate.Decide(s.ID, "call:operator", "approve"); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if !got.ok || !strings.Contains(got.content, "override succeeded; exact tool call rerun once") || tool.overrideCalls != 1 {
		t.Fatalf("result=%#v override calls=%d", got, tool.overrideCalls)
	}
}

type emptyOverrideTool struct{ overrideTestTool }

func (t *emptyOverrideTool) CallAsOperator(context.Context, *session.Session, map[string]any) (string, error) {
	t.overrideCalls++
	return "", nil
}

func TestSuccessfulEmptyOperatorOverrideIsUnambiguous(t *testing.T) {
	bus := events.NewBus()
	eventCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	cfg := config.Defaults(t.TempDir())
	tool := &emptyOverrideTool{overrideTestTool{name: "list_dir"}}
	runner := &Runner{bus: bus, tools: tools.New(tool), cfg: func() config.Config { return cfg }}
	runner.gate = NewGate(bus, runner.cfg)
	s := &session.Session{ID: "session", Workspace: t.TempDir(), Run: session.RunState{Status: "running"}, ToolsEnabled: map[string]bool{"list_dir": true}}
	done := make(chan string, 1)
	go func() {
		content := runner.executeTool(context.Background(), s, "run", "call", "list_dir", map[string]any{}).Content
		done <- content
	}()
	<-eventCh
	if err := runner.gate.Decide(s.ID, "call:operator", "approve"); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if !strings.Contains(got, "override succeeded") || !strings.Contains(got, "completed with no output") || strings.Contains(got, "Access to the path is denied") {
		t.Fatalf("ambiguous empty override result: %q", got)
	}
}
