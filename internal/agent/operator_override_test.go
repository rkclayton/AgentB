package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
	"harness/internal/tools"
)

type countedIdentityTool struct {
	tool          tools.Tool
	normalCalls   int
	overrideCalls int
}

func (t *countedIdentityTool) Name() string           { return t.tool.Name() }
func (t *countedIdentityTool) Description() string    { return t.tool.Description() }
func (t *countedIdentityTool) Schema() map[string]any { return t.tool.Schema() }
func (t *countedIdentityTool) Call(ctx context.Context, s *session.Session, args map[string]any) (string, error) {
	return t.tool.Call(ctx, s, args)
}
func (t *countedIdentityTool) CallDetailed(ctx context.Context, s *session.Session, args map[string]any) tools.CallDetail {
	t.normalCalls++
	return t.tool.(tools.DetailedTool).CallDetailed(ctx, s, args)
}
func (t *countedIdentityTool) CallAsOperator(ctx context.Context, s *session.Session, args map[string]any) (string, error) {
	t.overrideCalls++
	return t.tool.(tools.OperatorOverrideTool).CallAsOperator(ctx, s, args)
}

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
	testOperatorOverrideRequiresApproval(t, config.ApprovalModeOff)
}

func TestOperatorOverrideRequiresApprovalInBoundaryOnlyMode(t *testing.T) {
	testOperatorOverrideRequiresApproval(t, config.ApprovalModeBoundaryOnly)
}

func testOperatorOverrideRequiresApproval(t *testing.T, approvalMode string) {
	t.Helper()
	bus := events.NewBus()
	eventCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	cfg := config.Defaults(t.TempDir())
	cfg.Approval.Mode = approvalMode
	tool := &overrideTestTool{}
	runner := &Runner{bus: bus, tools: tools.New(tool), cfg: func() config.Config { return cfg }}
	runner.gate = NewGate(bus, runner.cfg)
	s := &session.Session{ID: "session", Workspace: t.TempDir(), Run: session.RunState{Status: "running"}, ToolsEnabled: map[string]bool{"shell": true}}
	type result struct {
		content         string
		ok              bool
		operatorContext bool
	}
	done := make(chan result, 1)
	go func() {
		outcome := runner.executeTool(context.Background(), s, "run", "call", "shell", map[string]any{"command": "Set-Content protected.txt value"})
		done <- result{content: outcome.Content, ok: outcome.OK, operatorContext: outcome.OperatorContext}
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
	if !ok || data["call_id"] != "call:operator" || data["name"] != "shell.operator_override" || data["boundary_escape"] != true {
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
		if !got.ok || !got.operatorContext || got.content != "operator-identity override succeeded; exact command rerun once:\nexit=0\noperator-ok" {
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
	cfg.Approval.Mode = config.ApprovalModeOff
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
	cfg.Approval.Mode = config.ApprovalModeOff
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

func TestBoundaryOnlyIdentityWrappedFileDenialRequiresOneExplicitOperatorRetry(t *testing.T) {
	for _, decision := range []string{"deny", "approve"} {
		t.Run(decision, func(t *testing.T) {
			workspace := t.TempDir()
			bus := events.NewBus()
			eventCh, unsubscribe := bus.Subscribe()
			defer unsubscribe()
			cfg := config.Defaults(workspace)
			cfg.Approval.Mode = config.ApprovalModeBoundaryOnly
			cfg.Shell.ServiceAccount.Enabled = true
			identity := tools.NewFileIdentity(nil)
			identity.Configure(cfg)
			coordinator := tools.NewFileCoordinator(session.NewWorkspaceRegistry(), func(id string) string { return id }, bus)
			wrapped := &countedIdentityTool{tool: identity.Wrap(tools.NewWriteFile(coordinator))}
			runner := &Runner{bus: bus, tools: tools.New(wrapped), cfg: func() config.Config { return cfg }}
			runner.gate = NewGate(bus, runner.cfg)
			s := &session.Session{
				ID:           "session",
				Workspace:    workspace,
				Run:          session.RunState{Status: "running"},
				ToolsEnabled: map[string]bool{"write_file": true},
				LastSeen:     map[string]time.Time{},
			}
			path := filepath.Join(workspace, "identity-write.txt")
			done := make(chan tools.CallOutcome, 1)
			go func() {
				done <- runner.executeTool(context.Background(), s, "run", "call", "write_file", map[string]any{"path": path, "content": "operator write"})
			}()

			var required events.Event
			select {
			case required = <-eventCh:
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for boundary escape")
			}
			data, ok := required.Data.(map[string]any)
			if required.Type != events.ApprovalRequired || !ok || data["boundary_escape"] != true || data["call_id"] != "call:operator" {
				t.Fatalf("first approval event=%#v", required)
			}
			if wrapped.normalCalls != 1 || wrapped.overrideCalls != 0 {
				t.Fatalf("before decision normal=%d operator=%d", wrapped.normalCalls, wrapped.overrideCalls)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("file changed before decision: %v", err)
			}
			if err := runner.gate.Decide(s.ID, "call:operator", decision); err != nil {
				t.Fatal(err)
			}
			var outcome tools.CallOutcome
			select {
			case outcome = <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for boundary decision")
			}
			if decision == "deny" {
				if outcome.OK || outcome.OperatorContext || wrapped.overrideCalls != 0 {
					t.Fatalf("denied outcome=%+v operator calls=%d", outcome, wrapped.overrideCalls)
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("denied call changed file: %v", err)
				}
				assertNoAdditionalApprovalRequired(t, eventCh)
				return
			}
			if !outcome.OK || !outcome.OperatorContext || wrapped.overrideCalls != 1 {
				t.Fatalf("approved outcome=%+v operator calls=%d", outcome, wrapped.overrideCalls)
			}
			content, err := os.ReadFile(path)
			if err != nil || string(content) != "operator write" {
				t.Fatalf("operator write content=%q err=%v", content, err)
			}
			assertNoAdditionalApprovalRequired(t, eventCh)
		})
	}
}

func assertNoAdditionalApprovalRequired(t *testing.T, eventCh <-chan events.Event) {
	t.Helper()
	additional := 0
	for {
		select {
		case event := <-eventCh:
			if event.Type == events.ApprovalRequired {
				additional++
			}
		default:
			if additional != 0 {
				t.Fatalf("received %d additional approval requests", additional)
			}
			return
		}
	}
}
