package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesConfigFromExample(t *testing.T) {
	dir := t.TempDir()
	examplePath := filepath.Join(dir, "harness.example.json")
	configPath := filepath.Join(dir, "harness.json")
	example := Defaults("./workspace")
	if err := example.Save(examplePath); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatal(err)
	}

	got, migrated, created, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if migrated || !created {
		t.Fatalf("migrated=%v created=%v", migrated, created)
	}
	if reason := ProfileSetupReason(&got.Servers[0]); reason != "model is empty; set it in Servers" {
		t.Fatalf("setup reason = %q", reason)
	}
	written, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, want) {
		t.Fatal("created config is not an exact copy of the example")
	}

	_, _, created, err = Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("existing config reported as created")
	}
}

func TestFileRoutingGuardDefaultsOnAndCanBeDisabled(t *testing.T) {
	cfg := Config{Shell: Shell{Command: []string{"unused"}}}
	ApplyDefaults(&cfg)
	if !cfg.Shell.FileRoutingGuardEnabled() {
		t.Fatal("file routing guard did not default on")
	}
	disabled := false
	cfg.Shell.FileRoutingGuard = &disabled
	ApplyDefaults(&cfg)
	if cfg.Shell.FileRoutingGuardEnabled() {
		t.Fatal("explicitly disabled file routing guard was re-enabled")
	}
}

func TestServiceAccountSplitDefaultsOffWithLocalAccountDefaults(t *testing.T) {
	cfg := Config{Shell: Shell{Command: []string{"unused"}}}
	ApplyDefaults(&cfg)
	if cfg.Shell.ServiceAccount.Enabled {
		t.Fatal("service-account split defaulted on")
	}
	if cfg.Shell.OperatorContext {
		t.Fatal("operator context defaulted on")
	}
	if cfg.Shell.OperatorContextTimeoutMinutes != 60 {
		t.Fatalf("operator context timeout=%d, want 60", cfg.Shell.OperatorContextTimeoutMinutes)
	}
	if cfg.Shell.ServiceAccount.Account != "agentb-svc" || cfg.Shell.ServiceAccount.Domain != "." {
		t.Fatalf("service-account defaults = %+v", cfg.Shell.ServiceAccount)
	}
}

func TestOperatorContextNeverPersistsOrRestartsEnabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	cfg := Defaults(t.TempDir())
	cfg.Shell.OperatorContext = true
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"operator_context": true`)) {
		t.Fatal("operator context was persisted on")
	}
	loaded, _, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Shell.OperatorContext {
		t.Fatal("operator context restarted on")
	}
}
