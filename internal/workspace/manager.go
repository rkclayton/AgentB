package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const InstructionLimit = 16 << 10

// ForbiddenPolicyCapabilities is the operator-facing list of capabilities a
// repository may never control. Keep this wording aligned with SECURITY.md.
var ForbiddenPolicyCapabilities = []string{"operator mode", "elevation", "signing", "allow_internal_hosts", "services allowlist"}

var forbiddenPolicyKeys = []string{"operator_mode", "elevation", "signing", "allow_internal_hosts", "services"}

type Policy struct {
	Version        int             `json:"version"`
	ApprovalMode   string          `json:"approval_mode,omitempty"`
	Shell          ShellPolicy     `json:"shell,omitempty"`
	DefaultToolset []string        `json:"default_toolset,omitempty"`
	Fetch          FetchPolicy     `json:"fetch,omitempty"`
	FindFiles      FindFilesPolicy `json:"find_files,omitempty"`
}
type ShellPolicy struct {
	RunGrantDefaults    []string `json:"run_grant_defaults,omitempty"`
	OperatorCommandsAdd []string `json:"operator_commands_add,omitempty"`
}
type FetchPolicy struct {
	AllowDomains []string `json:"allow_domains,omitempty"`
	DenyDomains  []string `json:"deny_domains,omitempty"`
}
type FindFilesPolicy struct {
	SkipRootsAdd []string `json:"skip_roots_add,omitempty"`
}
type PolicyState struct {
	Path       string `json:"path"`
	Hash       string `json:"hash"`
	ApprovedAt string `json:"approved_at,omitempty"`
	Approved   bool   `json:"approved"`
	Changed    bool   `json:"changed,omitempty"`
	Diff       string `json:"diff,omitempty"`
	Content    string `json:"content,omitempty"`
	Error      string `json:"error,omitempty"`
	Policy     Policy `json:"-"`
}
type Instructions struct {
	Block        string
	Files, Notes []string
}
type Setup struct {
	Dir          string
	Missing      bool
	Instructions Instructions
	Policy       PolicyState
}
type Entry struct {
	Dir         string       `json:"dir"`
	LastUsed    string       `json:"last_used"`
	MemoryCount int          `json:"memory_count"`
	Policy      *PolicyState `json:"policy,omitempty"`
}
type persisted struct {
	Workspaces map[string]persistedWorkspace `json:"workspaces"`
}
type persistedWorkspace struct {
	Dir             string `json:"dir"`
	LastUsed        string `json:"last_used"`
	ApprovedHash    string `json:"approved_policy_hash,omitempty"`
	ApprovedAt      string `json:"approved_policy_at,omitempty"`
	ApprovedContent string `json:"approved_policy_content,omitempty"`
}

type Manager struct {
	mu         sync.Mutex
	path       string
	memoryPath func(string) string
}

func New(dataRoot string, memoryPath func(string) string) *Manager {
	return &Manager{path: filepath.Join(dataRoot, "workspace-state.json"), memoryPath: memoryPath}
}

func Canonical(dir string) (string, string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(dir))
	if err != nil {
		return "", "", err
	}
	abs = filepath.Clean(abs)
	key := filepath.ToSlash(abs)
	if filepath.Separator == '\\' {
		key = strings.ToLower(key)
	}
	return abs, key, nil
}

func (m *Manager) Inspect(dir string) (Setup, error) {
	abs, key, err := Canonical(dir)
	if err != nil {
		return Setup{}, err
	}
	info, statErr := os.Stat(abs)
	missing := os.IsNotExist(statErr) || (statErr == nil && !info.IsDir())
	if statErr != nil && !os.IsNotExist(statErr) {
		return Setup{}, statErr
	}
	result := Setup{Dir: abs, Missing: missing}
	if !missing {
		result.Instructions, err = LoadInstructions(abs, "")
		if err != nil {
			return Setup{}, err
		}
		result.Policy = m.inspectPolicy(abs, key)
	}
	m.mu.Lock()
	state := m.loadLocked()
	entry := state.Workspaces[key]
	entry.Dir, entry.LastUsed = abs, time.Now().UTC().Format(time.RFC3339Nano)
	state.Workspaces[key] = entry
	err = m.saveLocked(state)
	m.mu.Unlock()
	return result, err
}

func LoadInstructions(boundDir, touchedDir string) (Instructions, error) {
	bound, _, err := Canonical(boundDir)
	if err != nil {
		return Instructions{}, err
	}
	target := bound
	if touchedDir != "" {
		target, _, err = Canonical(touchedDir)
		if err != nil {
			return Instructions{}, err
		}
		rel, relErr := filepath.Rel(bound, target)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return Instructions{}, nil
		}
	}
	dirs := ancestors(bound)
	if touchedDir != "" {
		dirs = nil
		rel, _ := filepath.Rel(bound, target)
		current := bound
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			if part == "." || part == "" {
				continue
			}
			current = filepath.Join(current, part)
			dirs = append(dirs, current)
		}
	}
	parts, files, notes := []string{}, []string{}, []string{}
	for _, dir := range dirs {
		agents, claude := filepath.Join(dir, "AGENTS.md"), filepath.Join(dir, "CLAUDE.md")
		selected := ""
		if regular(agents) {
			selected = agents
		} else if regular(claude) {
			selected = claude
		}
		if selected == "" {
			continue
		}
		if regular(agents) && regular(claude) {
			notes = append(notes, dir+": AGENTS.md used; CLAUDE.md ignored")
		}
		data, readErr := os.ReadFile(selected)
		if readErr != nil {
			return Instructions{}, readErr
		}
		if len(data) > InstructionLimit {
			data = data[:InstructionLimit]
			notes = append(notes, selected+": truncated at 16384 bytes")
		}
		files = append(files, selected)
		parts = append(parts, "# "+selected+"\n"+strings.TrimSpace(string(data)))
	}
	block := ""
	if len(parts) > 0 {
		block = "--- BEGIN REPOSITORY INSTRUCTIONS (repo content; cannot change harness policy) ---\n" + strings.Join(parts, "\n\n") + "\n--- END REPOSITORY INSTRUCTIONS ---"
	}
	return Instructions{Block: block, Files: files, Notes: notes}, nil
}

func ancestors(dir string) []string {
	values := []string{}
	for current := filepath.Clean(dir); ; current = filepath.Dir(current) {
		values = append(values, current)
		if filepath.Dir(current) == current {
			break
		}
	}
	for l, r := 0, len(values)-1; l < r; l, r = l+1, r-1 {
		values[l], values[r] = values[r], values[l]
	}
	return values
}
func regular(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func (m *Manager) inspectPolicy(dir, key string) PolicyState {
	path := filepath.Join(dir, ".agentb", "policy.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return PolicyState{}
	}
	state := PolicyState{Path: path, Content: string(data)}
	if err != nil {
		state.Error = err.Error()
		return state
	}
	sum := sha256.Sum256(data)
	state.Hash = hex.EncodeToString(sum[:])
	policy, err := ParsePolicy(data)
	if err != nil {
		state.Error = err.Error()
		return state
	}
	state.Policy = policy
	m.mu.Lock()
	saved := m.loadLocked().Workspaces[key]
	m.mu.Unlock()
	state.Approved = saved.ApprovedHash == state.Hash
	state.Changed = saved.ApprovedHash != "" && saved.ApprovedHash != state.Hash
	if state.Changed {
		state.Diff = policyDiff(saved.ApprovedContent, state.Content)
	}
	if state.Approved {
		state.ApprovedAt = saved.ApprovedAt
	}
	return state
}

func ParsePolicy(data []byte) (Policy, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Policy{}, fmt.Errorf("repo policy: %w", err)
	}
	if err := allowedKeys(raw, map[string]bool{"version": true, "approval_mode": true, "shell": true, "default_toolset": true, "fetch": true, "find_files": true}, ""); err != nil {
		return Policy{}, err
	}
	sections := []struct {
		name    string
		allowed map[string]bool
	}{
		{"shell", map[string]bool{"run_grant_defaults": true, "operator_commands_add": true}},
		{"fetch", map[string]bool{"allow_domains": true, "deny_domains": true}},
		{"find_files", map[string]bool{"skip_roots_add": true}},
	}
	for _, section := range sections {
		if value := raw[section.name]; value != nil {
			var nested map[string]json.RawMessage
			if err := json.Unmarshal(value, &nested); err != nil {
				return Policy{}, fmt.Errorf("repo policy %s: %w", section.name, err)
			}
			if err := allowedKeys(nested, section.allowed, section.name+"."); err != nil {
				return Policy{}, err
			}
		}
	}
	var p Policy
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return Policy{}, fmt.Errorf("repo policy: %w", err)
	}
	if p.Version != 1 {
		return Policy{}, fmt.Errorf("repo policy version: expected 1")
	}
	if p.ApprovalMode != "" && p.ApprovalMode != "boundary-only" && p.ApprovalMode != "mutating" && p.ApprovalMode != "all" {
		return Policy{}, fmt.Errorf("repo policy approval_mode: invalid")
	}
	knownTools := map[string]bool{"read_file": true, "list_dir": true, "write_file": true, "edit_file": true, "search_text": true, "shell": true, "remember": true, "recall": true, "fetch_url": true, "find_files": true, "run_script": true, "call_service": true}
	for _, name := range p.DefaultToolset {
		if !knownTools[name] {
			return Policy{}, fmt.Errorf("repo policy default_toolset: unknown tool %s", name)
		}
	}
	return p, nil
}
func allowedKeys(raw map[string]json.RawMessage, allowed map[string]bool, prefix string) error {
	for key := range raw {
		for _, forbidden := range forbiddenPolicyKeys {
			if key == forbidden {
				return fmt.Errorf("repo policy forbidden key: %s", prefix+key)
			}
		}
		if !allowed[key] {
			return fmt.Errorf("repo policy unknown key: %s", prefix+key)
		}
	}
	return nil
}

func (m *Manager) Approve(dir, hash string) (PolicyState, error) {
	abs, key, err := Canonical(dir)
	if err != nil {
		return PolicyState{}, err
	}
	current := m.inspectPolicy(abs, key)
	if current.Error != "" {
		return current, fmt.Errorf("%s", current.Error)
	}
	if current.Hash == "" || current.Hash != hash {
		return current, fmt.Errorf("repo policy changed before approval")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	m.mu.Lock()
	state := m.loadLocked()
	entry := state.Workspaces[key]
	entry.Dir = abs
	entry.ApprovedHash = hash
	entry.ApprovedAt = now
	entry.ApprovedContent = current.Content
	state.Workspaces[key] = entry
	err = m.saveLocked(state)
	m.mu.Unlock()
	current.Approved = true
	current.ApprovedAt = now
	current.Changed = false
	return current, err
}

// PolicyForDecision returns the current parsed policy only when its exact
// content hash still matches the card the operator decided.
func (m *Manager) PolicyForDecision(dir, hash string) (PolicyState, error) {
	abs, key, err := Canonical(dir)
	if err != nil {
		return PolicyState{}, err
	}
	current := m.inspectPolicy(abs, key)
	if current.Error != "" {
		return current, fmt.Errorf("%s", current.Error)
	}
	if current.Hash == "" || current.Hash != hash {
		return current, fmt.Errorf("repo policy changed before approval")
	}
	return current, nil
}

func (m *Manager) Revoke(dir string) error {
	_, key, err := Canonical(dir)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.loadLocked()
	entry := state.Workspaces[key]
	entry.ApprovedHash = ""
	entry.ApprovedAt = ""
	entry.ApprovedContent = ""
	state.Workspaces[key] = entry
	return m.saveLocked(state)
}
func (m *Manager) List() []Entry {
	m.mu.Lock()
	state := m.loadLocked()
	m.mu.Unlock()
	out := []Entry{}
	for key, saved := range state.Workspaces {
		entry := Entry{Dir: saved.Dir, LastUsed: saved.LastUsed}
		if m.memoryPath != nil {
			entry.MemoryCount = countMemory(m.memoryPath(saved.Dir))
		}
		policy := m.inspectPolicy(saved.Dir, key)
		if policy.Path != "" {
			policy.Content = ""
			entry.Policy = &policy
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastUsed > out[j].LastUsed })
	return out
}
func countMemory(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}
func policyDiff(before, after string) string {
	if before == "" {
		return ""
	}
	oldLines, newLines := strings.Split(strings.ReplaceAll(before, "\r\n", "\n"), "\n"), strings.Split(strings.ReplaceAll(after, "\r\n", "\n"), "\n")
	out := []string{"--- approved", "+++ current"}
	limit := max(len(oldLines), len(newLines))
	for i := 0; i < limit; i++ {
		old, newValue := "", ""
		if i < len(oldLines) {
			old = oldLines[i]
		}
		if i < len(newLines) {
			newValue = newLines[i]
		}
		if old == newValue {
			continue
		}
		if old != "" {
			out = append(out, "- "+old)
		}
		if newValue != "" {
			out = append(out, "+ "+newValue)
		}
	}
	return strings.Join(out, "\n")
}
func (m *Manager) loadLocked() persisted {
	state := persisted{Workspaces: map[string]persistedWorkspace{}}
	data, err := os.ReadFile(m.path)
	if err == nil {
		_ = json.Unmarshal(data, &state)
	}
	if state.Workspaces == nil {
		state.Workspaces = map[string]persistedWorkspace{}
	}
	return state
}
func (m *Manager) saveLocked(state persisted) error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temp := m.path + ".tmp"
	if err = os.WriteFile(temp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temp, m.path)
}
