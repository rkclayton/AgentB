package agent

import (
	"context"
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
