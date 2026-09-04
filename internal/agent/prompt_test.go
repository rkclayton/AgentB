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
