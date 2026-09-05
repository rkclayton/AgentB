package web

import (
	"fmt"
	"testing"

	"harness/internal/config"
)

func TestFailedProbePreservesPreviousTimestamp(t *testing.T) {
	profile := &config.Profile{Capabilities: config.Capabilities{ProbedAt: "2026-09-04T12:00:00Z", Server: "llama.cpp"}}
	caps, findings := failedProbeCapabilities(profile, fmt.Errorf("connection refused"))
	if caps.ProbedAt != profile.Capabilities.ProbedAt || caps.Server != "llama.cpp" {
		t.Fatalf("failed probe capabilities=%+v", caps)
	}
	if len(findings) != 1 || findings[0] != "probe failed: connection refused" {
		t.Fatalf("findings=%v", findings)
	}
}
