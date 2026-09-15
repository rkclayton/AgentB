package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
	"harness/internal/tools"
)

func TestNonzeroShellExitProducesFailedToolResultEvent(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults(root)
	command := "printf ui-stderr-marker >&2; exit 9"
	if runtime.GOOS == "windows" {
		command = "[Console]::Error.WriteLine('ui-stderr-marker'); exit 9"
	} else {
		cfg.Shell.Command = []string{"sh", "-c"}
	}
	runner := &Runner{bus: events.NewBus(), tools: tools.New(tools.NewShell(cfg.Shell)), cfg: func() config.Config { return cfg }}
	runner.gate = NewGate(runner.bus, runner.cfg)
	item := &session.Session{ID: "test", Workspace: root, ToolsEnabled: map[string]bool{"shell": true}}
	outcome := runner.executeTool(context.Background(), item, "run", "call", "shell", map[string]any{"command": command})
	if outcome.OK || !strings.Contains(outcome.Content, "exit=9") || !strings.Contains(outcome.Content, "ui-stderr-marker") {
		t.Fatalf("model outcome=%+v", outcome)
	}
	data := toolResultEventData(1, "call", "shell", outcome.Content, outcome.OK, outcome.OperatorContext, outcome.Untrusted, 1, 1, outcome.Metadata)
	if data["ok"] != false || !strings.Contains(data["preview"].(string), "exit=9") {
		t.Fatalf("UI event data=%#v", data)
	}
}

func TestProducedFileMetadataTracksOnlyJailedFileTools(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "reports", "done.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("done"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &session.Session{Workspace: workspace}
	for _, name := range []string{"write_file", "edit_file"} {
		metadata := producedFileMetadata(s, name, map[string]any{"path": "reports/done.txt"})
		file, _ := metadata["file"].(map[string]any)
		if file["path"] != "reports/done.txt" || file["bytes"] != int64(4) {
			t.Fatalf("%s metadata=%#v", name, metadata)
		}
	}
	planRoot := t.TempDir()
	planDir := filepath.Join(planRoot, "stable")
	if err := os.MkdirAll(filepath.Join(planDir, "reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "reports", "done.txt"), []byte("plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &session.Session{Role: "d", Workspace: workspace, PlansRoot: planRoot, PlanID: "stable", PlanDir: planDir}
	dMetadata := producedFileMetadata(d, "write_file", map[string]any{"path": "reports/done.txt"})
	dFile, _ := dMetadata["file"].(map[string]any)
	if dFile["path"] != "reports/done.txt" || dFile["bytes"] != int64(4) {
		t.Fatalf("d metadata=%#v", dMetadata)
	}
	if metadata := producedFileMetadata(s, "run_script", map[string]any{"path": "reports/done.txt"}); metadata != nil {
		t.Fatalf("run_script unexpectedly tracked: %#v", metadata)
	}
	if metadata := producedFileMetadata(s, "write_file", map[string]any{"path": filepath.Join(t.TempDir(), "outside.txt")}); metadata != nil {
		t.Fatalf("outside path unexpectedly tracked: %#v", metadata)
	}
}

func TestSandboxExecutionTargetIsToolResultMetadata(t *testing.T) {
	workspace := t.TempDir()
	cfg := config.Defaults(workspace)
	cfg.Sandbox.Enabled = true
	s := &session.Session{Workspace: workspace}
	for _, test := range []struct {
		name string
		args map[string]any
	}{{"shell", map[string]any{"command": "uname -s"}}, {"run_script", map[string]any{"language": "bash", "source": "uname -s"}}} {
		metadata := sandboxResultMetadata(cfg, s, test.name, test.args)
		data := toolResultEventData(1, "call", test.name, "target", true, true, false, 1, 1, metadata)
		if target, ok := data["target"].(string); !ok || !strings.HasPrefix(target, "sandbox agentb-") {
			t.Fatalf("%s data=%#v", test.name, data)
		}
	}
}
