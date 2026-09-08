package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestLoadWithTemplateSeparatesApplicationAndDataRoots(t *testing.T) {
	applicationRoot := t.TempDir()
	dataRoot := filepath.Join(t.TempDir(), "nested", "data")
	templatePath := filepath.Join(applicationRoot, "harness.example.json")
	configPath := filepath.Join(dataRoot, "harness.json")
	example := Defaults(t.TempDir())
	if err := example.Save(templatePath); err != nil {
		t.Fatal(err)
	}
	loaded, _, created, err := LoadWithTemplate(configPath, templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !created || loaded.Listen != example.Listen {
		t.Fatalf("created=%v loaded=%+v", created, loaded)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("live config was not created in data root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(applicationRoot, "harness.json")); !os.IsNotExist(err) {
		t.Fatalf("live config was written into application root: %v", err)
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

func TestOperatorFilesDefaultOffWithThirtyDayRetention(t *testing.T) {
	cfg := Defaults(t.TempDir())
	if cfg.OperatorFiles.AllowMailboxApprovals || cfg.OperatorFiles.LogRetentionDays != 30 {
		t.Fatalf("operator files defaults = %+v", cfg.OperatorFiles)
	}
	cfg.OperatorFiles.LogRetentionDays = 3651
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "operator_files.log_retention_days") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestServicesAdditiveCurrentSchemaDefaultsEmpty(t *testing.T) {
	cfg := Defaults(t.TempDir())
	if cfg.ConfigVersion != CurrentConfigVersion || cfg.Services == nil || len(cfg.Services) != 0 {
		t.Fatalf("defaults version=%d services=%#v", cfg.ConfigVersion, cfg.Services)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	delete(document, "services")
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var omitted Config
	if err := json.Unmarshal(data, &omitted); err != nil {
		t.Fatal(err)
	}
	ApplyDefaults(&omitted)
	if omitted.ConfigVersion != CurrentConfigVersion || omitted.Services == nil || len(omitted.Services) != 0 {
		t.Fatalf("omitted services=%#v version=%d", omitted.Services, omitted.ConfigVersion)
	}
	if err := omitted.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestChatAutoRenameDefaultsOnButPersistsOff(t *testing.T) {
	cfg := Defaults(t.TempDir())
	if !cfg.Chat.AutoRename {
		t.Fatal("default auto rename is off")
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	delete(document, "chat")
	data, _ = json.Marshal(document)
	var absent Config
	if err := json.Unmarshal(data, &absent); err != nil {
		t.Fatal(err)
	}
	ApplyDefaults(&absent)
	if !absent.Chat.AutoRename {
		t.Fatal("absent chat config did not default on")
	}
	data, _ = json.Marshal(map[string]any{"auto_rename": false})
	var disabled Chat
	if err := json.Unmarshal(data, &disabled); err != nil {
		t.Fatal(err)
	}
	if disabled.AutoRename {
		t.Fatal("explicit off was not preserved")
	}
}

func TestServiceAllowlistValidation(t *testing.T) {
	valid := Service{BaseURL: "https://broker.example/api", Auth: "exec:entra-token --scope broker", AllowedMethods: []string{"GET", "post"}, TimeoutS: 30, MaxBodyKB: 256, RequireConfirmation: true}
	cfg := Defaults(t.TempDir())
	cfg.Services["deploy-broker"] = valid
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*Service)
		want   string
	}{
		{"base_url", func(service *Service) { service.BaseURL = "file:///tmp/broker" }, "base_url"},
		{"auth", func(service *Service) { service.Auth = "oauth:magic" }, "auth"},
		{"static_env", func(service *Service) { service.Auth = "static_bearer:not-valid" }, "environment variable"},
		{"methods", func(service *Service) { service.AllowedMethods = nil }, "allowed_methods"},
		{"timeout", func(service *Service) { service.TimeoutS = 0 }, "timeout_s"},
		{"body_limit", func(service *Service) { service.MaxBodyKB = 0 }, "max_body_kb"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cfg
			service := valid
			test.mutate(&service)
			candidate.Services = map[string]Service{"deploy-broker": service}
			if err := candidate.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
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
		Deliver Deliver `json:"deliver"`
		Shell   struct {
			OperatorContextIdleTimeoutMinutes int `json:"operator_context_idle_timeout_minutes"`
		} `json:"shell"`
		Tools struct {
			Shell ShellTool `json:"shell"`
			Fetch struct {
				DenyDomains []string `json:"deny_domains"`
			} `json:"fetch"`
			FindFiles struct {
				SkipRoots []string `json:"skip_roots"`
			} `json:"find_files"`
		} `json:"tools"`
		Servers []Profile `json:"servers"`
		Agents  []Agent   `json:"agents"`
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
	if document.Deliver.Mode != DeliverModeBoth || document.Deliver.ExchangeFolder != `%USERPROFILE%\Agent_b` {
		t.Fatalf("template delivery=%+v", document.Deliver)
	}
	if document.Shell.OperatorContextIdleTimeoutMinutes != 20 {
		t.Fatalf("template operator idle timeout=%d, want literal 20", document.Shell.OperatorContextIdleTimeoutMinutes)
	}
	if len(document.Tools.Shell.OperatorCommands) != 1 || document.Tools.Shell.OperatorCommands[0] != "git" {
		t.Fatalf("template operator commands=%v, want [git]", document.Tools.Shell.OperatorCommands)
	}
	if len(document.Tools.Fetch.DenyDomains) != 8 || len(document.Tools.FindFiles.SkipRoots) != 5 {
		t.Fatalf("template policy defaults: deny_domains=%v skip_roots=%v", document.Tools.Fetch.DenyDomains, document.Tools.FindFiles.SkipRoots)
	}
	if len(document.Servers) != 0 || len(document.Agents) != 0 {
		t.Fatalf("first-run template must have no configured profiles or agents: servers=%d agents=%+v", len(document.Servers), document.Agents)
	}
}

func TestEmptyServerListIsValidFirstRunState(t *testing.T) {
	cfg := Defaults(t.TempDir())
	cfg.Servers = []Profile{}
	cfg.Agents = []Agent{}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Agents = []Agent{{Name: "Local", B: "missing", Toolset: FullToolset()}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "agents") {
		t.Fatalf("nonempty first-run agent error=%v", err)
	}
}

func TestDeliveryDefaultsOnlyWhenOmitted(t *testing.T) {
	cfg := Defaults(t.TempDir())
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	delete(document, "deliver")
	data, _ = json.Marshal(document)
	var omitted Config
	if err := json.Unmarshal(data, &omitted); err != nil {
		t.Fatal(err)
	}
	ApplyDefaults(&omitted)
	if omitted.Deliver.Mode != DeliverModeBoth || omitted.Deliver.ExchangeFolder == "" {
		t.Fatalf("omitted delivery did not default: %+v", omitted.Deliver)
	}

	document["deliver"] = map[string]any{"mode": "", "exchange_folder": ""}
	data, _ = json.Marshal(document)
	var explicit Config
	if err := json.Unmarshal(data, &explicit); err != nil {
		t.Fatal(err)
	}
	ApplyDefaults(&explicit)
	if explicit.Deliver.Mode != "" || explicit.Deliver.ExchangeFolder != "" {
		t.Fatalf("explicit empties were defaulted: %+v", explicit.Deliver)
	}
	if err := explicit.Validate(); err == nil || !strings.Contains(err.Error(), "deliver.mode") {
		t.Fatalf("explicit empty validation=%v", err)
	}
}

func TestPolicyListsDefaultOnlyWhenOmitted(t *testing.T) {
	omitted := Defaults(t.TempDir())
	omitted.Tools.Fetch.DenyDomains = nil
	omitted.Tools.FindFiles.SkipRoots = nil
	omitted.Tools.Shell.OperatorCommands = nil
	applyDefaults(&omitted)
	if len(omitted.Tools.Fetch.DenyDomains) != 8 || len(omitted.Tools.FindFiles.SkipRoots) != 5 || len(omitted.Tools.Shell.OperatorCommands) != 1 || omitted.Tools.Shell.OperatorCommands[0] != "git" {
		t.Fatalf("omitted defaults: deny=%v skip=%v operator=%v", omitted.Tools.Fetch.DenyDomains, omitted.Tools.FindFiles.SkipRoots, omitted.Tools.Shell.OperatorCommands)
	}

	cleared := Defaults(t.TempDir())
	cleared.Tools.Fetch.DenyDomains = []string{}
	cleared.Tools.FindFiles.SkipRoots = []string{}
	cleared.Tools.Shell.OperatorCommands = []string{}
	applyDefaults(&cleared)
	if cleared.Tools.Fetch.DenyDomains == nil || len(cleared.Tools.Fetch.DenyDomains) != 0 || cleared.Tools.FindFiles.SkipRoots == nil || len(cleared.Tools.FindFiles.SkipRoots) != 0 || cleared.Tools.Shell.OperatorCommands == nil || len(cleared.Tools.Shell.OperatorCommands) != 0 {
		t.Fatalf("explicit clears were replaced: deny=%#v skip=%#v operator=%#v", cleared.Tools.Fetch.DenyDomains, cleared.Tools.FindFiles.SkipRoots, cleared.Tools.Shell.OperatorCommands)
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
	if len(loaded.LoadNotices) != 4 || loaded.LoadNotices[0] != OperatorIdleTimeoutMigrationNotice || loaded.LoadNotices[1] != ByteWindowMigrationNotice || loaded.LoadNotices[2] != ModelRolesMigrationNotice || loaded.LoadNotices[3] != AgentObjectsMigrationNotice {
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

func TestVersion3LineLimitsMigrateToByteWindows(t *testing.T) {
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
	document["config_version"] = float64(3)
	toolSettings := document["tools"].(map[string]any)
	for _, name := range []string{"read_file", "fetch"} {
		settings := toolSettings[name].(map[string]any)
		settings["default_limit"] = float64(200)
		settings["max_limit"] = float64(400)
		settings["max_line_chars"] = float64(500)
	}
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
	if !migrated || loaded.ConfigVersion != CurrentConfigVersion || loaded.Tools.ReadFile.DefaultLimit != 16<<10 || loaded.Tools.ReadFile.MaxLimit != 64<<10 || loaded.Tools.Fetch.DefaultLimit != 16<<10 || loaded.Tools.Fetch.MaxLimit != 64<<10 {
		t.Fatalf("migrated=%t config=%+v", migrated, loaded.Tools)
	}
	if len(loaded.LoadNotices) != 3 || loaded.LoadNotices[0] != ByteWindowMigrationNotice || loaded.LoadNotices[1] != ModelRolesMigrationNotice || loaded.LoadNotices[2] != AgentObjectsMigrationNotice {
		t.Fatalf("migration notices=%#v", loaded.LoadNotices)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Tools map[string]map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(persisted, &saved); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"read_file", "fetch"} {
		if _, exists := saved.Tools[name]["max_line_chars"]; exists {
			t.Fatalf("legacy %s line cap persisted: %s", name, persisted)
		}
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

func TestReserveOutputUsesCanonicalDefault(t *testing.T) {
	cfg := Config{Servers: []Profile{{}}, Shell: Shell{Command: []string{"unused"}}}
	ApplyDefaults(&cfg)
	if got := cfg.Servers[0].Context.ReserveOutput; got != DefaultReserveOutput {
		t.Fatalf("reserve_output=%d, want canonical default %d", got, DefaultReserveOutput)
	}
}

func TestLegacyMigrationRejectsMalformedOptionalSections(t *testing.T) {
	for _, field := range []string{"sampling_thinking", "sampling_nonthinking", "thinking", "context"} {
		raw := []byte(fmt.Sprintf(`{"server":{},%q:"not-an-object"}`, field))
		if _, _, err := migrateV1(raw); err == nil || !strings.Contains(err.Error(), field) {
			t.Fatalf("field %s migration error=%v", field, err)
		}
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

func TestSchema4ModelProfilesMigrateWithUTF8BOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	cfg := Defaults(t.TempDir())
	cfg.Servers[0].ID = "small"
	cfg.Servers[0].Label = "Small"
	cfg.Servers[0].Model = ""
	main := defaultProfile()
	main.ID, main.Label, main.BaseURL, main.Model = "homepc", "HomePC", "http://127.0.0.1:8080", "model"
	main.Context.NCtx = 16384
	main.Capabilities.NCtx = 32768
	main.Capabilities.ToolCalls = true
	main.Capabilities.Streaming = true
	main.Capabilities.OverflowBehavior = "error"
	cfg.Servers = append(cfg.Servers, main)

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	document["config_version"] = float64(4)
	delete(document, "roles")
	for _, raw := range document["servers"].([]any) {
		profile := raw.(map[string]any)
		context := profile["context"].(map[string]any)
		context["n_ctx_override"] = context["n_ctx"]
		delete(context, "n_ctx")
		profile["api_key"] = ""
		delete(profile, "credential")
	}
	data, err = json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append([]byte{0xef, 0xbb, 0xbf}, data...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, migrated, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated || loaded.ConfigVersion != CurrentConfigVersion || len(loaded.Agents) != 1 || loaded.Agents[0].B != "homepc" || loaded.Agents[0].C != "" {
		t.Fatalf("migrated=%t version=%d agents=%+v", migrated, loaded.ConfigVersion, loaded.Agents)
	}
	if loaded.Servers[1].Context.NCtx != 16384 || loaded.Servers[1].Capabilities.NCtx != 32768 {
		t.Fatalf("profile context=%+v capabilities=%+v", loaded.Servers[1].Context, loaded.Servers[1].Capabilities)
	}
	if len(loaded.LoadNotices) != 2 || loaded.LoadNotices[0] != ModelRolesMigrationNotice || loaded.LoadNotices[1] != AgentObjectsMigrationNotice {
		t.Fatalf("notices=%#v", loaded.LoadNotices)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(persisted, []byte{0xef, 0xbb, 0xbf}) || bytes.Contains(persisted, []byte("n_ctx_override")) || bytes.Contains(persisted, []byte("api_key")) || bytes.Contains(persisted, []byte(`"roles"`)) {
		t.Fatalf("legacy fields survived migration: %s", persisted)
	}
}

func TestAgentProfilesUseLetteredSlots(t *testing.T) {
	cfg := Defaults(t.TempDir())
	c := cfg.Servers[0]
	c.ID, c.Label = "small", "Small"
	cfg.Servers = append(cfg.Servers, c)
	cfg.Agents[0].C = "small"
	agent, ok := cfg.Agent("local")
	if !ok || agent.B != "local" || agent.C != "small" || agent.D != "" {
		t.Fatalf("lettered agent=%+v ok=%t", agent, ok)
	}
}

func TestAgentNameReservesAgentA(t *testing.T) {
	cfg := Defaults(t.TempDir())
	for _, name := range []string{"agent_a", "Agent_A", "AGENT_A"} {
		cfg.Agents[0].Name = name
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "agent_a is reserved") {
			t.Fatalf("name=%q error=%v", name, err)
		}
	}
}

func TestProfileDecodePreservesExplicitNumericZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	document := `{
  "config_version": 5,
  "workspace": ".",
  "servers": [{
    "id": "local", "base_url": "http://127.0.0.1:8080", "model": "model",
    "sampling": {"thinking": {"temperature": 0, "top_k": 0, "repeat_penalty": 0}},
    "reasoning": {"enabled": false},
    "context": {"n_ctx": 8192, "reserve_output": 0}
  }],
  "roles": {"main": "local", "aux": ""}
}`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, _, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	profile := loaded.Servers[0]
	if profile.Context.ReserveOutput != 0 || profile.Sampling.Thinking.Temperature != 0 || profile.Sampling.Thinking.TopK != 0 || profile.Sampling.Thinking.RepeatPenalty != 0 || profile.Reasoning.Enabled {
		t.Fatalf("explicit zeros changed: %+v", profile)
	}
	if profile.Sampling.Thinking.TopP != .95 || profile.Sampling.Nonthinking.TopP != .8 || profile.Reasoning.Control != "auto" || profile.Reasoning.Effort != "medium" {
		t.Fatalf("omitted sampling defaults missing: %+v", profile.Sampling)
	}
}

func TestProfileCredentialNameIsValidated(t *testing.T) {
	cfg := Defaults(t.TempDir())
	cfg.Servers[0].Credential = "../outside"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "servers[0].credential") {
		t.Fatalf("invalid credential error=%v", err)
	}
}

func TestSchema4APIKeyMovesToNamedDPAPIStore(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DPAPI is Windows-only")
	}
	dir := t.TempDir()
	dataRoot := t.TempDir()
	path := filepath.Join(dir, "harness.json")
	cfg := Defaults(t.TempDir())
	cfg.ConfigVersion = 4
	cfg.Servers[0].Model = "model"
	cfg.Servers[0].Context.NCtx = 8192
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	delete(document, "roles")
	profile := document["servers"].([]any)[0].(map[string]any)
	profile["api_key"] = "migration-secret"
	delete(profile, "credential")
	context := profile["context"].(map[string]any)
	context["n_ctx_override"] = context["n_ctx"]
	delete(context, "n_ctx")
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, _, _, err := LoadWithRoots(path, filepath.Join(dir, "harness.example.json"), dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Servers[0].Credential != "local" || loaded.Servers[0].APIKey != "migration-secret" || loaded.Masked().Servers[0].APIKey != "•••• set" {
		t.Fatalf("loaded profile=%+v masked=%+v", loaded.Servers[0], loaded.Masked().Servers[0])
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(persisted, []byte("migration-secret")) || bytes.Contains(persisted, []byte("api_key")) || !bytes.Contains(persisted, []byte(`"credential": "local"`)) {
		t.Fatalf("secret persisted in config: %s", persisted)
	}
	if _, err := os.Stat(filepath.Join(dataRoot, ".agentb-profile-credential-local.dpapi")); err != nil {
		t.Fatalf("named credential missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agentb-profile-credential-local.dpapi")); !os.IsNotExist(err) {
		t.Fatalf("credential was inferred beside config: %v", err)
	}
	reloaded, _, _, err := LoadWithRoots(path, filepath.Join(dir, "harness.example.json"), dataRoot)
	if err != nil || reloaded.Servers[0].APIKey != "migration-secret" {
		t.Fatalf("reloaded key=%q err=%v", reloaded.Servers[0].APIKey, err)
	}
}

func TestAttachmentConfigIsAdditiveCurrentSchema(t *testing.T) {
	cfg := Defaults(t.TempDir())
	if cfg.ConfigVersion != CurrentConfigVersion || cfg.Tools.Attachments.MaxBytes != 8<<20 {
		t.Fatalf("defaults: version=%d attachments=%+v", cfg.ConfigVersion, cfg.Tools.Attachments)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "harness.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	tools := document["tools"].(map[string]any)
	delete(tools, "attachments")
	without, _ := json.Marshal(document)
	var loaded Config
	if err := json.Unmarshal(without, &loaded); err != nil {
		t.Fatal(err)
	}
	ApplyDefaults(&loaded)
	if loaded.ConfigVersion != CurrentConfigVersion || loaded.Tools.Attachments.MaxBytes != 8<<20 {
		t.Fatalf("omitted attachment defaults=%+v version=%d", loaded.Tools.Attachments, loaded.ConfigVersion)
	}
	loaded.Tools.Attachments.MaxBytes = 0
	if err := loaded.Validate(); err == nil || !strings.Contains(err.Error(), "tools.attachments.max_bytes") {
		t.Fatalf("zero limit validation=%v", err)
	}
}
