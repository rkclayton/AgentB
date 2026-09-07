package agent

import (
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/session"
)

func TestPromptIncludesOSDateAndContextWithoutInventingCity(t *testing.T) {
	renderer := &PromptRenderer{text: "date={{date}} context={{os_context}}"}
	profile := &config.Profile{}
	value := renderer.Render(profile, &session.Session{Workspace: "workspace"}, nil, "")
	if strings.Contains(value, "{{") || !strings.Contains(value, "date=") || !strings.Contains(value, "timezone") {
		t.Fatalf("OS context was not rendered: %q", value)
	}
}

func TestProjectInstructionsSitAfterToolNamesAndBeforeMemory(t *testing.T) {
	renderer := &PromptRenderer{text: "prefix tools={{tools}}\n{{project}}\nbody\n{{memory}}"}
	profile := &config.Profile{}
	item := &session.Session{Workspace: "workspace", ProjectBlock: "PROJECT BLOCK"}
	base := renderer.RenderParts(profile, item, []string{"read_file"}, "", "")
	full := renderer.RenderParts(profile, item, []string{"read_file"}, item.ProjectBlock, "MEMORY BLOCK")
	toolsAt, projectAt, memoryAt := strings.Index(full, "tools=read_file"), strings.Index(full, "PROJECT BLOCK"), strings.Index(full, "MEMORY BLOCK")
	if toolsAt < 0 || projectAt <= toolsAt || memoryAt <= projectAt {
		t.Fatalf("prompt order=%q", full)
	}
	if delta := len(full) - len(base); delta != len("PROJECT BLOCK")+len("MEMORY BLOCK")+1 {
		t.Fatalf("byte delta=%d base=%q full=%q", delta, base, full)
	}
}
