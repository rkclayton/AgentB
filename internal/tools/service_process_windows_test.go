//go:build windows

package tools

import (
	"strings"
	"syscall"
	"testing"

	"harness/internal/config"
)

func TestServiceSpawnFailureClassification(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{syscall.Errno(1326), "authentication failed"},
		{syscall.Errno(1385), "logon right"},
		{syscall.Errno(5), "CreateProcessWithLogonW failed"},
	} {
		if got := serviceSpawnReason(classifyLogonFailure(test.err)); !strings.Contains(got, test.want) {
			t.Fatalf("classification for %v = %q, want %q", test.err, got, test.want)
		}
	}
}

func TestMinimalShellEnvironmentDoesNotInheritArbitraryVariables(t *testing.T) {
	t.Setenv("AGENTB_TEST_SECRET_SENTINEL", "must-not-pass")
	environment := minimalShellEnvironment(config.ShellServiceAccount{Account: "agentb-svc", Domain: "."}, `C:\workspace`)
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "AGENTB_TEST_SECRET_SENTINEL") || strings.Contains(joined, "must-not-pass") {
		t.Fatalf("environment inherited arbitrary variable: %s", joined)
	}
	for _, name := range []string{"PATH=", "TEMP=C:\\workspace", "USERNAME=agentb-svc"} {
		if !strings.Contains(joined, name) {
			t.Fatalf("environment omitted %q: %s", name, joined)
		}
	}
}
