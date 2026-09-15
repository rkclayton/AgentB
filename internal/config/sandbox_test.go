package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSandboxIsGlobalDefaultOnAndDeterministicPerMountSet(t *testing.T) {
	cfg := Defaults(t.TempDir())
	first, ok := cfg.SandboxTarget("s2", []string{`C:\scratch\s2`, `C:\repo-a`})
	second, again := cfg.SandboxTarget("s2", []string{`C:\scratch\s2`, `C:\repo-a`})
	if !ok || !again || first != second || !strings.HasPrefix(first, "agentb-") {
		t.Fatalf("targets=%q/%q ok=%t/%t", first, second, ok, again)
	}
	if changed, _ := cfg.SandboxTarget("s2", []string{`C:\scratch\s2`, `C:\repo-b`}); changed == first {
		t.Fatal("changed mount set reused its sandbox target")
	}
	cfg.Sandbox.Enabled = false
	if _, ok := cfg.SandboxTarget("s2", nil); ok {
		t.Fatal("disabled global sandbox produced a target")
	}
}

func TestSandboxLegacyMapMigratesWithoutSurvivingMarshal(t *testing.T) {
	for _, test := range []struct {
		raw     string
		enabled bool
	}{
		{`{"workspaces":{"C:\\repo":true}}`, true},
		{`{"workspaces":{"C:\\repo":false}}`, false},
		{`{}`, true},
	} {
		var sandbox Sandbox
		if err := json.Unmarshal([]byte(test.raw), &sandbox); err != nil {
			t.Fatal(err)
		}
		if sandbox.Enabled != test.enabled {
			t.Fatalf("%s enabled=%t", test.raw, sandbox.Enabled)
		}
		encoded, err := json.Marshal(sandbox)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "workspaces") {
			t.Fatalf("legacy map survived: %s", encoded)
		}
	}
}
