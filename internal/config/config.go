package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
	ConfigVersion int           `json:"config_version"`
	Listen        string        `json:"listen"`
	Workspace     string        `json:"workspace"`
	LogDir        string        `json:"log_dir"`
	Servers       []Profile     `json:"servers"`
	Run           RunConfig     `json:"run"`
	Approval      Approval      `json:"approval"`
	Context       GlobalContext `json:"context"`
	Memory        Memory        `json:"memory"`
	Tools         Tools         `json:"tools"`
	Shell         Shell         `json:"shell"`
	LoadNotices   []string      `json:"-"`
}

type Profile struct {
	ID                   string       `json:"id"`
	Label                string       `json:"label"`
	BaseURL              string       `json:"base_url"`
	Model                string       `json:"model"`
	APIKey               string       `json:"api_key"`
	RequestTimeoutS      int          `json:"request_timeout_s"`
	ProbeMode            string       `json:"probe_mode"`
	Sampling             SamplingPair `json:"sampling"`
	Reasoning            Reasoning    `json:"reasoning"`
	Context              Context      `json:"context"`
	SystemPromptOverride string       `json:"system_prompt_override"`
	Capabilities         Capabilities `json:"capabilities"`
}

type SamplingPair struct{ Thinking, Nonthinking Sampling }

func (s SamplingPair) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Thinking    Sampling `json:"thinking"`
		Nonthinking Sampling `json:"nonthinking"`
	}{s.Thinking, s.Nonthinking})
}
func (s *SamplingPair) UnmarshalJSON(data []byte) error {
	var v struct {
		Thinking    Sampling `json:"thinking"`
		Nonthinking Sampling `json:"nonthinking"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	s.Thinking = v.Thinking
	s.Nonthinking = v.Nonthinking
	return nil
}

type Sampling struct {
	Temperature     float64 `json:"temperature"`
	TopP            float64 `json:"top_p"`
	TopK            int     `json:"top_k"`
	MinP            float64 `json:"min_p"`
	PresencePenalty float64 `json:"presence_penalty"`
	RepeatPenalty   float64 `json:"repeat_penalty"`
}
type Reasoning struct {
	Control      string   `json:"control"`
	Enabled      bool     `json:"enabled"`
	Effort       string   `json:"effort"`
	ValidEfforts []string `json:"valid_efforts"`
	Preserve     bool     `json:"preserve"`
}
type Context struct {
	NCtxOverride  int `json:"n_ctx_override"`
	ReserveOutput int `json:"reserve_output"`
}
type Capabilities struct {
	Server             string   `json:"server"`
	Props              bool     `json:"props"`
	NCtx               int      `json:"n_ctx"`
	Tokenize           bool     `json:"tokenize"`
	ApplyTemplate      bool     `json:"apply_template"`
	ApplyTemplateTools bool     `json:"apply_template_tools"`
	Streaming          bool     `json:"streaming"`
	ToolCalls          bool     `json:"tool_calls"`
	GrammarConstrained bool     `json:"grammar_constrained"`
	CachedTokens       bool     `json:"cached_tokens"`
	Timings            bool     `json:"timings"`
	PromptProgress     bool     `json:"prompt_progress"`
	ReasoningControl   string   `json:"reasoning_control"`
	ValidEfforts       []string `json:"valid_efforts"`
	OverflowBehavior   string   `json:"overflow_behavior"`
	ProbedAt           string   `json:"probed_at"`
	Findings           []string `json:"findings"`
}
type RunConfig struct {
	MaxTurns                 int `json:"max_turns"`
	CycleWindow              int `json:"cycle_window"`
	MaxConsecutiveToolErrors int `json:"max_consecutive_tool_errors"`
	MaxConcurrent            int `json:"max_concurrent"`
	QueueDepth               int `json:"queue_depth"`
}
type Approval struct {
	Mode string `json:"mode"`
}

const (
	CurrentConfigVersion     = 4
	DefaultReserveOutput     = 10240
	ApprovalModeBoundaryOnly = "boundary-only"
	ApprovalModeMutating     = "mutating"
	ApprovalModeAll          = "all"
	ApprovalModeOff          = "off"
)

const ApprovalDefaultMigrationNotice = "corrected inherited approval default from mutating to boundary-only; mutating can be reselected in Settings > Run & approval"
const OperatorIdleTimeoutMigrationNotice = "migrated shell.operator_context_timeout_minutes to shell.operator_context_idle_timeout_minutes; operator mode now expires after agent inactivity"
const ByteWindowMigrationNotice = "migrated read_file and fetch_url limits from line counts to UTF-8 byte windows"

type GlobalContext struct {
	SoftPct    float64 `json:"soft_pct"`
	SummaryPct float64 `json:"summary_pct"`
	Accounting string  `json:"accounting"`
}
type Memory struct {
	Enabled   bool   `json:"enabled"`
	Dir       string `json:"dir"`
	MaxTokens int    `json:"max_tokens"`
}
type Tools struct {
	ReadFile ReadFileTool `json:"read_file"`
	ListDir  ListDirTool  `json:"list_dir"`
	Grep     GrepTool     `json:"grep"`
	Fetch    FetchTool    `json:"fetch"`
}
type ReadFileTool struct {
	DefaultLimit int `json:"default_limit"`
	MaxLimit     int `json:"max_limit"`
}
type ListDirTool struct {
	MaxEntries int      `json:"max_entries"`
	Ignore     []string `json:"ignore"`
}
type GrepTool struct {
	MaxMatches   int `json:"max_matches"`
	MaxLineChars int `json:"max_line_chars"`
}
type FetchTool struct {
	TimeoutS           int      `json:"timeout_s"`
	MaxBytes           int64    `json:"max_bytes"`
	MaxRedirects       int      `json:"max_redirects"`
	DefaultLimit       int      `json:"default_limit"`
	MaxLimit           int      `json:"max_limit"`
	AllowDomains       []string `json:"allow_domains"`
	AllowInternalHosts []string `json:"allow_internal_hosts"`
}
type Shell struct {
	Command            []string `json:"command"`
	TimeoutS           int      `json:"timeout_s"`
	MaxTimeoutS        int      `json:"max_timeout_s"`
	MaxOutputLinesHead int      `json:"max_output_lines_head"`
	MaxOutputLinesTail int      `json:"max_output_lines_tail"`
	FileRoutingGuard   *bool    `json:"file_routing_guard"`
	// OperatorContext is exposed to the UI but Save always persists it as false.
	OperatorContext                   bool                `json:"operator_context"`
	OperatorContextExpiresAt          string              `json:"-"`
	OperatorContextIdleTimeoutMinutes int                 `json:"operator_context_idle_timeout_minutes"`
	ServiceAccount                    ShellServiceAccount `json:"service_account"`
	Deny                              []string            `json:"deny"`
}

type ShellServiceAccount struct {
	Enabled bool   `json:"enabled"`
	Account string `json:"account"`
	Domain  string `json:"domain"`
}

func Defaults(workspace string) Config {
	if workspace == "" {
		workspace = "./workspace"
	}
	abs, _ := filepath.Abs(workspace)
	thinking := Sampling{Temperature: .6, TopP: .95, TopK: 20, RepeatPenalty: 1}
	nonthinking := Sampling{Temperature: .7, TopP: .8, TopK: 20, PresencePenalty: 1.5, RepeatPenalty: 1}
	return Config{
		ConfigVersion: CurrentConfigVersion,
		Listen:        "127.0.0.1:8790", Workspace: abs, LogDir: "logs",
		Servers: []Profile{{ID: "local", Label: "Local", BaseURL: "http://127.0.0.1:8080", Model: "", RequestTimeoutS: 900, ProbeMode: "full", Sampling: SamplingPair{Thinking: thinking, Nonthinking: nonthinking}, Reasoning: Reasoning{Control: "auto", Enabled: true, Effort: "medium", ValidEfforts: []string{}}, Context: Context{ReserveOutput: DefaultReserveOutput}, Capabilities: Capabilities{ValidEfforts: []string{}, Findings: []string{}}}},
		Run:     RunConfig{MaxTurns: 40, CycleWindow: 8, MaxConsecutiveToolErrors: 3, MaxConcurrent: 2}, Approval: Approval{Mode: ApprovalModeBoundaryOnly}, Context: GlobalContext{SoftPct: .75, SummaryPct: .85, Accounting: "auto"}, Memory: Memory{Enabled: true, Dir: "memory", MaxTokens: 1500},
		Tools: Tools{ReadFile: ReadFileTool{DefaultLimit: 16 << 10, MaxLimit: 64 << 10}, ListDir: ListDirTool{MaxEntries: 300, Ignore: []string{".git", "node_modules", "__pycache__", "vendor", "bin", "obj", "dist", ".venv"}}, Grep: GrepTool{MaxMatches: 50, MaxLineChars: 200}, Fetch: FetchTool{TimeoutS: 20, MaxBytes: 2 << 20, MaxRedirects: 5, DefaultLimit: 16 << 10, MaxLimit: 64 << 10, AllowDomains: []string{}, AllowInternalHosts: []string{}}},
		Shell: Shell{Command: []string{"powershell", "-NoProfile", "-NonInteractive", "-Command"}, TimeoutS: 60, MaxTimeoutS: 600, MaxOutputLinesHead: 60, MaxOutputLinesTail: 40, OperatorContextIdleTimeoutMinutes: 20, Deny: []string{"rm -rf /", "format ", "diskpart", "shutdown", "Remove-Item -Recurse -Force C:\\"}, FileRoutingGuard: boolPointer(true), ServiceAccount: ShellServiceAccount{Account: "agentb-svc", Domain: "."}},
	}
}

func Load(path string) (*Config, bool, bool, error) {
	return LoadWithTemplate(path, filepath.Join(filepath.Dir(path), "harness.example.json"))
}

// LoadWithTemplate loads the live configuration from path and, on first run,
// materializes it from the explicitly supplied application template.
func LoadWithTemplate(path, examplePath string) (*Config, bool, bool, error) {
	data, err := os.ReadFile(path)
	created := false
	if os.IsNotExist(err) {
		data, err = os.ReadFile(examplePath)
		if err != nil {
			return nil, false, false, fmt.Errorf("%s is missing; read %s: %w", filepath.Base(path), filepath.Base(examplePath), err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, false, false, fmt.Errorf("create config directory: %w", err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return nil, false, false, fmt.Errorf("create %s from %s: %w", filepath.Base(path), filepath.Base(examplePath), err)
		}
		created = true
	}
	if err != nil {
		return nil, false, false, err
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	var metadata struct {
		ConfigVersion *int `json:"config_version"`
		Approval      struct {
			Mode string `json:"mode"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, false, created, err
	}
	unstamped := metadata.ConfigVersion == nil
	if !unstamped && *metadata.ConfigVersion != 2 && *metadata.ConfigVersion != 3 && *metadata.ConfigVersion != CurrentConfigVersion {
		return nil, false, created, fmt.Errorf("config_version: unsupported value %d (current %d)", *metadata.ConfigVersion, CurrentConfigVersion)
	}
	migrated, data, err := migrateV1(data)
	if err != nil {
		return nil, false, created, err
	}
	version := 0
	if metadata.ConfigVersion != nil {
		version = *metadata.ConfigVersion
	}
	schemaMigrated, idleTimeoutRemapped, data, err := migrateOperatorIdleTimeout(data, version)
	if err != nil {
		return nil, false, created, err
	}
	byteWindowMigrated, data, err := migrateByteWindows(data, version)
	if err != nil {
		return nil, false, created, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, false, created, err
	}
	// Operator context is a launch-scoped state, never a startup instruction.
	cfg.Shell.OperatorContext = false
	cfg.Shell.OperatorContextExpiresAt = ""
	applyDefaults(&cfg)
	approvalDefaultCorrected := unstamped && metadata.Approval.Mode == ApprovalModeMutating
	if approvalDefaultCorrected {
		cfg.Approval.Mode = ApprovalModeBoundaryOnly
	}
	if err := cfg.Validate(); err != nil {
		return nil, false, created, err
	}
	if migrated || schemaMigrated || byteWindowMigrated || unstamped {
		if err := cfg.Save(path); err != nil {
			return nil, false, created, err
		}
	}
	if approvalDefaultCorrected {
		cfg.LoadNotices = append(cfg.LoadNotices, ApprovalDefaultMigrationNotice)
	}
	if idleTimeoutRemapped {
		cfg.LoadNotices = append(cfg.LoadNotices, OperatorIdleTimeoutMigrationNotice)
	}
	if byteWindowMigrated && !unstamped {
		cfg.LoadNotices = append(cfg.LoadNotices, ByteWindowMigrationNotice)
	}
	return &cfg, migrated || schemaMigrated || byteWindowMigrated, created, nil
}

func (c Config) Save(path string) error {
	persisted := c
	persisted.ConfigVersion = CurrentConfigVersion
	if err := persisted.Validate(); err != nil {
		return err
	}
	persisted.Shell.OperatorContext = false
	persisted.Shell.OperatorContextExpiresAt = ""
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

var slug = regexp.MustCompile(`^[a-z0-9-]+$`)

func (c Config) Validate() error {
	if c.ConfigVersion != CurrentConfigVersion {
		return fmt.Errorf("config_version: unsupported value %d (current %d)", c.ConfigVersion, CurrentConfigVersion)
	}
	if c.Listen == "" {
		return fmt.Errorf("listen: required")
	}
	if c.Workspace == "" {
		return fmt.Errorf("workspace: required")
	}
	if len(c.Servers) == 0 {
		return fmt.Errorf("servers: at least one required")
	}
	seen := map[string]bool{}
	for i, p := range c.Servers {
		prefix := fmt.Sprintf("servers[%d]", i)
		if !slug.MatchString(p.ID) {
			return fmt.Errorf("%s.id: must be a slug", prefix)
		}
		if seen[p.ID] {
			return fmt.Errorf("%s.id: duplicate", prefix)
		}
		seen[p.ID] = true
		if p.ProbeMode != "full" && p.ProbeMode != "minimal" && p.ProbeMode != "off" {
			return fmt.Errorf("%s.probe_mode: invalid", prefix)
		}
		if p.RequestTimeoutS < 1 {
			return fmt.Errorf("%s.request_timeout_s: must be positive", prefix)
		}
		if !oneOf(p.Reasoning.Control, "auto", "chat_template_kwargs", "top_level", "server_flag", "none") {
			return fmt.Errorf("%s.reasoning.control: invalid", prefix)
		}
		if len(p.Reasoning.ValidEfforts) > 0 && !contains(p.Reasoning.ValidEfforts, p.Reasoning.Effort) {
			return fmt.Errorf("%s.reasoning.effort: not in valid_efforts", prefix)
		}
		if p.Context.NCtxOverride < 0 {
			return fmt.Errorf("%s.context.n_ctx_override: cannot be negative", prefix)
		}
		if p.Context.ReserveOutput < 0 {
			return fmt.Errorf("%s.context.reserve_output: cannot be negative", prefix)
		}
		if p.Capabilities.Props && p.Context.NCtxOverride > p.Capabilities.NCtx {
			return fmt.Errorf("%s.context.n_ctx_override: may only lower probed n_ctx", prefix)
		}
		if p.ProbeMode == "off" && p.Context.NCtxOverride == 0 {
			return fmt.Errorf("%s.context.n_ctx_override: required when probe_mode is off", prefix)
		}
	}
	if c.Run.MaxTurns < 1 {
		return fmt.Errorf("run.max_turns: must be positive")
	}
	if c.Run.MaxConcurrent < 1 {
		return fmt.Errorf("run.max_concurrent: must be positive")
	}
	if c.Run.QueueDepth < 0 {
		return fmt.Errorf("run.queue_depth: cannot be negative")
	}
	if c.Run.CycleWindow < 0 {
		return fmt.Errorf("run.cycle_window: cannot be negative")
	}
	if c.Run.MaxConsecutiveToolErrors < 0 {
		return fmt.Errorf("run.max_consecutive_tool_errors: cannot be negative")
	}
	if !oneOf(c.Approval.Mode, ApprovalModeBoundaryOnly, ApprovalModeMutating, ApprovalModeAll, ApprovalModeOff) {
		return fmt.Errorf("approval.mode: invalid")
	}
	if !(c.Context.SoftPct > 0 && c.Context.SoftPct < c.Context.SummaryPct && c.Context.SummaryPct <= 1) {
		return fmt.Errorf("context: require soft_pct < summary_pct <= 1")
	}
	if !oneOf(c.Context.Accounting, "auto", "exact", "estimated") {
		return fmt.Errorf("context.accounting: invalid")
	}
	if c.Memory.MaxTokens < 0 {
		return fmt.Errorf("memory.max_tokens: cannot be negative")
	}
	if c.Memory.Dir == "" {
		return fmt.Errorf("memory.dir: required")
	}
	if c.Tools.ReadFile.DefaultLimit < 1 {
		return fmt.Errorf("tools.read_file.default_limit: must be positive")
	}
	if c.Tools.ReadFile.MaxLimit < 1 {
		return fmt.Errorf("tools.read_file.max_limit: must be positive")
	}
	if c.Tools.ListDir.MaxEntries < 1 {
		return fmt.Errorf("tools.list_dir.max_entries: must be positive")
	}
	if c.Tools.Grep.MaxMatches < 1 || c.Tools.Grep.MaxLineChars < 1 {
		return fmt.Errorf("tools.grep: limits must be positive")
	}
	if c.Tools.Fetch.TimeoutS < 1 || c.Tools.Fetch.TimeoutS > 300 {
		return fmt.Errorf("tools.fetch.timeout_s: must be between 1 and 300")
	}
	if c.Tools.Fetch.MaxBytes < 1 || c.Tools.Fetch.MaxBytes > 64<<20 {
		return fmt.Errorf("tools.fetch.max_bytes: must be between 1 and 67108864")
	}
	if c.Tools.Fetch.MaxRedirects < 0 || c.Tools.Fetch.MaxRedirects > 20 {
		return fmt.Errorf("tools.fetch.max_redirects: must be between 0 and 20")
	}
	if c.Tools.Fetch.DefaultLimit < 1 || c.Tools.Fetch.MaxLimit < 1 || c.Tools.Fetch.DefaultLimit > c.Tools.Fetch.MaxLimit {
		return fmt.Errorf("tools.fetch: byte limits must be positive and default_limit no greater than max_limit")
	}
	for _, host := range append(append([]string{}, c.Tools.Fetch.AllowDomains...), c.Tools.Fetch.AllowInternalHosts...) {
		if !validFetchHost(host) {
			return fmt.Errorf("tools.fetch: allow-list entries must be hostnames or IP addresses without schemes, ports, paths, or wildcards")
		}
	}
	if len(c.Shell.Command) == 0 {
		return fmt.Errorf("shell.command: required")
	}
	if c.Shell.TimeoutS < 1 || c.Shell.MaxTimeoutS < c.Shell.TimeoutS {
		return fmt.Errorf("shell.timeout_s: must be positive and no greater than max_timeout_s")
	}
	if c.Shell.MaxOutputLinesHead < 0 || c.Shell.MaxOutputLinesTail < 0 {
		return fmt.Errorf("shell: output line limits cannot be negative")
	}
	if c.Shell.OperatorContextIdleTimeoutMinutes < 1 || c.Shell.OperatorContextIdleTimeoutMinutes > 1440 {
		return fmt.Errorf("shell.operator_context_idle_timeout_minutes: must be between 1 and 1440")
	}
	if strings.TrimSpace(c.Shell.ServiceAccount.Account) == "" {
		return fmt.Errorf("shell.service_account.account: required")
	}
	if strings.TrimSpace(c.Shell.ServiceAccount.Domain) == "" {
		return fmt.Errorf("shell.service_account.domain: required")
	}
	return nil
}

// ProfileSetupReason explains incomplete first-run profile settings without
// making the configuration file itself invalid.
func ProfileSetupReason(profile *Profile) string {
	if strings.TrimSpace(profile.BaseURL) == "" {
		return "base_url is empty; set it in Servers"
	}
	if strings.TrimSpace(profile.Model) == "" {
		return "model is empty; set it in Servers"
	}
	return ""
}

func applyDefaults(c *Config) {
	d := Defaults(c.Workspace)
	if c.ConfigVersion == 0 {
		c.ConfigVersion = CurrentConfigVersion
	}
	if c.Listen == "" {
		c.Listen = d.Listen
	}
	if c.LogDir == "" {
		c.LogDir = d.LogDir
	}
	if c.Workspace == "" {
		c.Workspace = d.Workspace
	}
	if c.Run.MaxTurns == 0 {
		c.Run.MaxTurns = d.Run.MaxTurns
	}
	if c.Run.MaxConcurrent == 0 {
		c.Run.MaxConcurrent = d.Run.MaxConcurrent
	}
	if c.Approval.Mode == "" {
		c.Approval = d.Approval
	}
	if c.Context.SoftPct == 0 {
		c.Context.SoftPct = d.Context.SoftPct
	}
	if c.Context.SummaryPct == 0 {
		c.Context.SummaryPct = d.Context.SummaryPct
	}
	if c.Context.Accounting == "" {
		c.Context.Accounting = d.Context.Accounting
	}
	if c.Memory.Dir == "" {
		c.Memory = d.Memory
	}
	if c.Tools.ReadFile.DefaultLimit == 0 {
		c.Tools = d.Tools
	} else if c.Tools.Fetch.TimeoutS == 0 {
		c.Tools.Fetch = d.Tools.Fetch
	}
	if len(c.Shell.Command) == 0 {
		c.Shell = d.Shell
	}
	if c.Shell.FileRoutingGuard == nil {
		c.Shell.FileRoutingGuard = boolPointer(true)
	}
	if c.Shell.OperatorContextIdleTimeoutMinutes == 0 {
		c.Shell.OperatorContextIdleTimeoutMinutes = d.Shell.OperatorContextIdleTimeoutMinutes
	}
	if c.Shell.ServiceAccount.Account == "" {
		c.Shell.ServiceAccount.Account = d.Shell.ServiceAccount.Account
	}
	if c.Shell.ServiceAccount.Domain == "" {
		c.Shell.ServiceAccount.Domain = d.Shell.ServiceAccount.Domain
	}
	for i := range c.Servers {
		p := &c.Servers[i]
		pd := d.Servers[0]
		if p.RequestTimeoutS == 0 {
			p.RequestTimeoutS = 900
		}
		if p.ProbeMode == "" {
			p.ProbeMode = "full"
		}
		if p.Context.ReserveOutput == 0 {
			p.Context.ReserveOutput = pd.Context.ReserveOutput
		}
		if p.Label == "" {
			p.Label = p.ID
		}
		if p.Sampling.Thinking.TopP == 0 {
			p.Sampling = pd.Sampling
		}
		if p.Reasoning.Control == "" {
			p.Reasoning = pd.Reasoning
		}
		if p.Capabilities.ValidEfforts == nil {
			p.Capabilities.ValidEfforts = []string{}
		}
		if p.Capabilities.Findings == nil {
			p.Capabilities.Findings = []string{}
		}
	}
}

func ApplyDefaults(c *Config) { applyDefaults(c) }
func (s Shell) FileRoutingGuardEnabled() bool {
	return s.FileRoutingGuard == nil || *s.FileRoutingGuard
}
func boolPointer(value bool) *bool { return &value }
func validFetchHost(value string) bool {
	value = strings.TrimSpace(value)
	if net.ParseIP(value) != nil {
		return true
	}
	return value != "" && !strings.ContainsAny(value, `/\\:*?`) && !strings.Contains(value, "..")
}
func oneOf(v string, values ...string) bool {
	for _, x := range values {
		if v == x {
			return true
		}
	}
	return false
}
func contains(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

func (c Config) Masked() Config {
	out := c
	out.Servers = append([]Profile(nil), c.Servers...)
	for i := range out.Servers {
		if out.Servers[i].APIKey != "" {
			out.Servers[i].APIKey = "•••• set"
		}
	}
	return out
}
