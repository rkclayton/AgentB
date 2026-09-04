package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeConfigFixture(t *testing.T, path, mode string, stamped bool) {
	t.Helper()
	cfg := Defaults(t.TempDir())
	cfg.Approval.Mode = mode
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if !stamped {
		delete(document, "config_version")
	}
	data, err = json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

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

func TestApprovalModeDefaultsWhenAbsentOrEmpty(t *testing.T) {
	for _, test := range []struct {
		name   string
		absent bool
	}{
		{name: "absent", absent: true},
		{name: "empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "harness.json")
			data, err := json.Marshal(Defaults(t.TempDir()))
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			if test.absent {
				delete(document, "approval")
			} else {
				document["approval"] = map[string]any{"mode": ""}
			}
			data, err = json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, _, _, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Approval.Mode != ApprovalModeBoundaryOnly {
				t.Fatalf("approval mode=%q, want %q", cfg.Approval.Mode, ApprovalModeBoundaryOnly)
			}
		})
	}
}

func TestHarnessExampleShipsBoundaryOnlyIndependentlyOfDefaults(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "harness.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		ConfigVersion int `json:"config_version"`
		Approval      struct {
			Mode string `json:"mode"`
		} `json:"approval"`
		Shell struct {
			OperatorContextIdleTimeoutMinutes int `json:"operator_context_idle_timeout_minutes"`
		} `json:"shell"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.ConfigVersion != CurrentConfigVersion {
		t.Fatalf("template config_version=%d, want %d", document.ConfigVersion, CurrentConfigVersion)
	}
	if document.Approval.Mode != "boundary-only" {
		t.Fatalf("template approval mode=%q, want literal boundary-only", document.Approval.Mode)
	}
	if document.Shell.OperatorContextIdleTimeoutMinutes != 20 {
		t.Fatalf("template operator idle timeout=%d, want literal 20", document.Shell.OperatorContextIdleTimeoutMinutes)
	}
}

func TestOperatorIdleTimeoutSchemaMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	cfg := Defaults(t.TempDir())
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	document["config_version"] = float64(2)
	shell := document["shell"].(map[string]any)
	delete(shell, "operator_context_idle_timeout_minutes")
	shell["operator_context_timeout_minutes"] = float64(37)
	data, err = json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, migrated, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated || loaded.ConfigVersion != CurrentConfigVersion || loaded.Shell.OperatorContextIdleTimeoutMinutes != 37 {
		t.Fatalf("migrated=%t version=%d idle=%d", migrated, loaded.ConfigVersion, loaded.Shell.OperatorContextIdleTimeoutMinutes)
	}
	if len(loaded.LoadNotices) != 1 || loaded.LoadNotices[0] != OperatorIdleTimeoutMigrationNotice {
		t.Fatalf("migration notices=%#v", loaded.LoadNotices)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(persisted, []byte(`operator_context_timeout_minutes`)) || !bytes.Contains(persisted, []byte(`"operator_context_idle_timeout_minutes": 37`)) {
		t.Fatalf("persisted migration=%s", persisted)
	}
	reloaded, migrated, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if migrated || len(reloaded.LoadNotices) != 0 || reloaded.Shell.OperatorContextIdleTimeoutMinutes != 37 {
		t.Fatalf("second load migrated=%t notices=%#v idle=%d", migrated, reloaded.LoadNotices, reloaded.Shell.OperatorContextIdleTimeoutMinutes)
	}
}

func TestCurrentOperatorIdleTimeoutConfigIsUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	cfg := Defaults(t.TempDir())
	cfg.Shell.OperatorContextIdleTimeoutMinutes = 11
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, migrated, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if migrated || loaded.Shell.OperatorContextIdleTimeoutMinutes != 11 || !bytes.Equal(before, after) {
		t.Fatalf("migrated=%t idle=%d changed=%t", migrated, loaded.Shell.OperatorContextIdleTimeoutMinutes, !bytes.Equal(before, after))
	}
}

func TestApprovalModeSchemaMigration(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		stamped    bool
		wantMode   string
		wantNotice bool
	}{
		{name: "unstamped inherited mutating", mode: ApprovalModeMutating, wantMode: ApprovalModeBoundaryOnly, wantNotice: true},
		{name: "stamped deliberate mutating", mode: ApprovalModeMutating, stamped: true, wantMode: ApprovalModeMutating},
		{name: "unstamped off alias", mode: ApprovalModeOff, wantMode: ApprovalModeOff},
		{name: "unstamped all", mode: ApprovalModeAll, wantMode: ApprovalModeAll},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "harness.json")
			writeConfigFixture(t, path, test.mode, test.stamped)
			cfg, _, _, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ConfigVersion != CurrentConfigVersion || cfg.Approval.Mode != test.wantMode {
				t.Fatalf("loaded version=%d mode=%q, want version=%d mode=%q", cfg.ConfigVersion, cfg.Approval.Mode, CurrentConfigVersion, test.wantMode)
			}
			if got := len(cfg.LoadNotices) == 1; got != test.wantNotice {
				t.Fatalf("notice present=%v, want %v: %#v", got, test.wantNotice, cfg.LoadNotices)
			}
			disk, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var persisted Config
			if err := json.Unmarshal(disk, &persisted); err != nil {
				t.Fatal(err)
			}
			if persisted.ConfigVersion != CurrentConfigVersion || persisted.Approval.Mode != test.wantMode {
				t.Fatalf("persisted version=%d mode=%q", persisted.ConfigVersion, persisted.Approval.Mode)
			}
			reloaded, _, _, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(reloaded.LoadNotices) != 0 {
				t.Fatalf("migration notice repeated: %#v", reloaded.LoadNotices)
			}
		})
	}
}

func TestSaveStampsConfigVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	cfg := Defaults(t.TempDir())
	cfg.ConfigVersion = 0
	cfg.Approval.Mode = ApprovalModeMutating
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, _, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ConfigVersion != CurrentConfigVersion || loaded.Approval.Mode != ApprovalModeMutating {
		t.Fatalf("round trip version=%d mode=%q", loaded.ConfigVersion, loaded.Approval.Mode)
	}
}

func TestLegacyV1WithoutApprovalDefaultsToBoundaryOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	document := map[string]any{
		"workspace": t.TempDir(),
		"server":    map[string]any{"base_url": "http://127.0.0.1:8080"},
	}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, migrated, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated || cfg.ConfigVersion != CurrentConfigVersion || cfg.Approval.Mode != ApprovalModeBoundaryOnly {
		t.Fatalf("migrated=%v version=%d mode=%q", migrated, cfg.ConfigVersion, cfg.Approval.Mode)
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
	if cfg.Shell.OperatorContextIdleTimeoutMinutes != 20 {
		t.Fatalf("operator context idle timeout=%d, want 20", cfg.Shell.OperatorContextIdleTimeoutMinutes)
	}
	if cfg.Shell.ServiceAccount.Account != "agentb-svc" || cfg.Shell.ServiceAccount.Domain != "." {
		t.Fatalf("service-account defaults = %+v", cfg.Shell.ServiceAccount)
	}
}

func TestFetchDefaultsAndAllowListValidation(t *testing.T) {
	cfg := Config{Tools: Tools{ReadFile: ReadFileTool{DefaultLimit: 1}}, Shell: Shell{Command: []string{"unused"}}}
	ApplyDefaults(&cfg)
	if cfg.Tools.Fetch.TimeoutS != 20 || cfg.Tools.Fetch.MaxBytes != 2<<20 || cfg.Tools.Fetch.MaxRedirects != 5 {
		t.Fatalf("fetch defaults = %+v", cfg.Tools.Fetch)
	}
	if len(cfg.Tools.Fetch.AllowDomains) != 0 || len(cfg.Tools.Fetch.AllowInternalHosts) != 0 {
		t.Fatalf("fetch allow lists should default empty: %+v", cfg.Tools.Fetch)
	}
	full := Defaults(t.TempDir())
	full.Tools.Fetch.AllowDomains = []string{"https://example.com"}
	if err := full.Validate(); err == nil {
		t.Fatal("fetch allow-list entry with scheme was accepted")
	}
	full = Defaults(t.TempDir())
	full.Tools.Fetch.AllowInternalHosts = []string{"::1"}
	if err := full.Validate(); err != nil {
		t.Fatalf("IPv6 internal host refused: %v", err)
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
