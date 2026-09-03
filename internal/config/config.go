package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

type Config struct {
	Listen    string        `json:"listen"`
	Workspace string        `json:"workspace"`
	LogDir    string        `json:"log_dir"`
	Servers   []Profile     `json:"servers"`
	Run       RunConfig     `json:"run"`
	Approval  Approval      `json:"approval"`
	Context   GlobalContext `json:"context"`
	Memory    Memory        `json:"memory"`
	Tools     Tools         `json:"tools"`
	Shell     Shell         `json:"shell"`
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
}
type ReadFileTool struct {
	DefaultLimit int `json:"default_limit"`
	MaxLimit     int `json:"max_limit"`
	MaxLineChars int `json:"max_line_chars"`
}
type ListDirTool struct {
	MaxEntries int      `json:"max_entries"`
	Ignore     []string `json:"ignore"`
}
type GrepTool struct {
	MaxMatches   int `json:"max_matches"`
	MaxLineChars int `json:"max_line_chars"`
}
type Shell struct {
	Command            []string `json:"command"`
	TimeoutS           int      `json:"timeout_s"`
	MaxTimeoutS        int      `json:"max_timeout_s"`
	MaxOutputLinesHead int      `json:"max_output_lines_head"`
	MaxOutputLinesTail int      `json:"max_output_lines_tail"`
	Deny               []string `json:"deny"`
}

func Defaults(workspace string) Config {
	if workspace == "" {
		workspace = "sandbox"
	}
	abs, _ := filepath.Abs(workspace)
	thinking := Sampling{Temperature: .6, TopP: .95, TopK: 20, RepeatPenalty: 1}
	nonthinking := Sampling{Temperature: .7, TopP: .8, TopK: 20, PresencePenalty: 1.5, RepeatPenalty: 1}
	return Config{
		Listen: "127.0.0.1:8790", Workspace: abs, LogDir: "logs",
		Servers: []Profile{{ID: "local", Label: "UD-Q3_K_XL local", BaseURL: "http://127.0.0.1:8080", Model: "qwen3.8-27b", RequestTimeoutS: 900, ProbeMode: "full", Sampling: SamplingPair{Thinking: thinking, Nonthinking: nonthinking}, Reasoning: Reasoning{Control: "auto", Enabled: true, Effort: "medium", ValidEfforts: []string{}}, Context: Context{ReserveOutput: 10240}, Capabilities: Capabilities{ValidEfforts: []string{}, Findings: []string{}}}},
		Run:     RunConfig{MaxTurns: 40, CycleWindow: 8, MaxConsecutiveToolErrors: 3, MaxConcurrent: 2}, Approval: Approval{Mode: "off"}, Context: GlobalContext{SoftPct: .75, SummaryPct: .85, Accounting: "auto"}, Memory: Memory{Enabled: true, Dir: "memory", MaxTokens: 1500},
		Tools: Tools{ReadFile: ReadFileTool{DefaultLimit: 200, MaxLimit: 400, MaxLineChars: 500}, ListDir: ListDirTool{MaxEntries: 300, Ignore: []string{".git", "node_modules", "__pycache__", "vendor", "bin", "obj", "dist", ".venv"}}, Grep: GrepTool{MaxMatches: 50, MaxLineChars: 200}},
		Shell: Shell{Command: []string{"powershell", "-NoProfile", "-NonInteractive", "-Command"}, TimeoutS: 60, MaxTimeoutS: 600, MaxOutputLinesHead: 60, MaxOutputLinesTail: 40, Deny: []string{"rm -rf /", "format ", "diskpart", "shutdown", "Remove-Item -Recurse -Force C:\\"}},
	}
}

func Load(path string) (*Config, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg := Defaults(filepath.Join(filepath.Dir(path), "sandbox"))
		if err := cfg.Save(path); err != nil {
			return nil, false, err
		}
		return &cfg, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	migrated, data, err := migrateV1(data)
	if err != nil {
		return nil, false, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, false, err
	}
	applyDefaults(&cfg)
	if err := cfg.Validate(); err != nil {
		return nil, false, err
	}
	if migrated {
		if err := cfg.Save(path); err != nil {
			return nil, false, err
		}
	}
	return &cfg, migrated, nil
}

func (c Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

var slug = regexp.MustCompile(`^[a-z0-9-]+$`)

func (c Config) Validate() error {
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
		if p.BaseURL == "" || p.Model == "" {
			return fmt.Errorf("%s: base_url and model required", prefix)
		}
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
		if p.Context.NCtxOverride < 0 || p.Context.ReserveOutput < 0 {
			return fmt.Errorf("%s.context: values cannot be negative", prefix)
		}
		if p.Capabilities.Props && p.Context.NCtxOverride > p.Capabilities.NCtx {
			return fmt.Errorf("%s.context.n_ctx_override: may only lower probed n_ctx", prefix)
		}
		if p.ProbeMode == "off" && p.Context.NCtxOverride == 0 {
			return fmt.Errorf("%s.context.n_ctx_override: required when probe_mode is off", prefix)
		}
	}
	if c.Run.MaxConcurrent < 1 || c.Run.QueueDepth < 0 || c.Run.CycleWindow < 0 || c.Run.MaxConsecutiveToolErrors < 0 {
		return fmt.Errorf("run: invalid limits")
	}
	if !oneOf(c.Approval.Mode, "off", "mutating", "all") {
		return fmt.Errorf("approval.mode: invalid")
	}
	if !(c.Context.SoftPct < c.Context.SummaryPct && c.Context.SummaryPct <= 1) {
		return fmt.Errorf("context: require soft_pct < summary_pct <= 1")
	}
	if !oneOf(c.Context.Accounting, "auto", "exact", "estimated") {
		return fmt.Errorf("context.accounting: invalid")
	}
	if c.Memory.MaxTokens < 0 {
		return fmt.Errorf("memory.max_tokens: cannot be negative")
	}
	return nil
}

func applyDefaults(c *Config) {
	d := Defaults(c.Workspace)
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
	}
	if len(c.Shell.Command) == 0 {
		c.Shell = d.Shell
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
			p.Context.ReserveOutput = 8192
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
