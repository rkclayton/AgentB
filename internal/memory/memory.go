package memory

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"harness/internal/config"
	"harness/internal/events"
)

type Counter func(context.Context, string, string) (int, error)
type Manager struct {
	baseDir string
	cfg     func() config.Config
	count   Counter
	mu      sync.Mutex
}

func New(baseDir string, cfg func() config.Config, count Counter) *Manager {
	return &Manager{baseDir: baseDir, cfg: cfg, count: count}
}
func (m *Manager) Dir() string {
	dir := m.cfg().Memory.Dir
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(m.baseDir, dir)
	}
	return filepath.Clean(dir)
}
func (m *Manager) Path(workspace string) string {
	abs, _ := filepath.Abs(workspace)
	clean := filepath.Clean(abs)
	canonical := filepath.ToSlash(clean)
	if filepath.Separator == '\\' {
		canonical = strings.ToLower(canonical)
	}
	sum := sha256.Sum256([]byte(canonical))
	name := filepath.Base(abs) + "-" + hex.EncodeToString(sum[:4]) + ".md"
	dir := m.Dir()
	path := filepath.Join(dir, name)
	// v0.9 and earlier hashed the display spelling. Keep that existing file
	// mapped to the default workspace instead of silently orphaning memory.
	legacySum := sha256.Sum256([]byte(clean))
	legacy := filepath.Join(dir, filepath.Base(abs)+"-"+hex.EncodeToString(legacySum[:4])+".md")
	if path != legacy {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if info, legacyErr := os.Stat(legacy); legacyErr == nil && info.Mode().IsRegular() {
				return legacy
			}
		}
	}
	return path
}
func (m *Manager) AgentPath(agentID string) string {
	return filepath.Join(m.Dir(), "agent-"+config.AgentID(agentID)+".md")
}
func (m *Manager) Load(ctx context.Context, workspace, serverID string) (string, string, error) {
	path := m.Path(workspace)
	return m.load(ctx, path, serverID, "Notes from earlier sessions in this workspace:")
}
func (m *Manager) LoadAgent(ctx context.Context, agentID, serverID string) (string, string, error) {
	path := m.AgentPath(agentID)
	return m.load(ctx, path, serverID, "Notes about how this agent works with the operator:")
}
func (m *Manager) load(ctx context.Context, path, serverID, heading string) (string, string, error) {
	if !m.cfg().Memory.Enabled {
		return "", path, nil
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", path, nil
	}
	if err != nil {
		return "", path, err
	}
	defer file.Close()
	lines := []string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "- ")
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 && len(parts[0]) == 10 && parts[0][4] == '-' && parts[0][7] == '-' {
			line = parts[1]
		}
		lines = append(lines, "- "+line)
	}
	if err := scanner.Err(); err != nil {
		return "", path, err
	}
	maxTokens := m.cfg().Memory.MaxTokens
	dropped := 0
	for len(lines) > 0 {
		body := memoryBlock(heading, lines, dropped)
		tokens, countErr := m.count(ctx, serverID, body)
		if countErr != nil {
			tokens = (len([]rune(body)) + 3) / 4
		}
		if maxTokens <= 0 || tokens <= maxTokens {
			return body, path, nil
		}
		lines = lines[1:]
		dropped++
	}
	return "", path, nil
}

func (m *Manager) Read(workspace string) (string, error) {
	return m.readPath(m.Path(workspace))
}
func (m *Manager) ReadAgent(agentID string) (string, error) {
	return m.readPath(m.AgentPath(agentID))
}
func (m *Manager) readPath(path string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimRight(normalize(string(data)), "\n"), nil
}

func (m *Manager) Clear(workspace string) error {
	return m.clearPath(m.Path(workspace))
}
func (m *Manager) ClearAgent(agentID string) error {
	return m.clearPath(m.AgentPath(agentID))
}

func (m *Manager) Count(workspace string) (int, error) {
	return m.countPath(m.Path(workspace))
}
func (m *Manager) CountAgent(agentID string) (int, error) {
	return m.countPath(m.AgentPath(agentID))
}
func (m *Manager) countPath(path string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(normalize(string(data)), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count, nil
}

func (m *Manager) DropSessionWrites(writes []events.MemoryWrite) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	byPath := map[string]map[string]bool{}
	root := m.Dir()
	for _, write := range writes {
		path := filepath.Clean(write.Path)
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return 0, fmt.Errorf("memory write path is outside the memory root")
		}
		if byPath[path] == nil {
			byPath[path] = map[string]bool{}
		}
		byPath[path][strings.TrimSpace(write.Note)] = true
	}
	dropped := 0
	for path, notes := range byPath {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return dropped, err
		}
		kept := []string{}
		for _, line := range strings.Split(strings.TrimRight(normalize(string(data)), "\n"), "\n") {
			plain := strings.TrimPrefix(strings.TrimSpace(line), "- ")
			parts := strings.SplitN(plain, " ", 2)
			note := plain
			if len(parts) == 2 && len(parts[0]) == 10 && parts[0][4] == '-' && parts[0][7] == '-' {
				note = parts[1]
			}
			if notes[strings.TrimSpace(note)] {
				dropped++
				continue
			}
			if strings.TrimSpace(line) != "" {
				kept = append(kept, line)
			}
		}
		if len(kept) == 0 {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return dropped, err
			}
			continue
		}
		temporary := path + ".tmp"
		if err := os.WriteFile(temporary, []byte(strings.Join(kept, "\n")+"\n"), 0o600); err != nil {
			return dropped, err
		}
		if err := os.Rename(temporary, path); err != nil {
			return dropped, err
		}
	}
	return dropped, nil
}
func (m *Manager) clearPath(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func memoryBlock(heading string, lines []string, dropped int) string {
	if len(lines) == 0 {
		return ""
	}
	out := []string{heading}
	if dropped > 0 {
		out = append(out, fmt.Sprintf("[%d older notes omitted; the file is over the memory budget — prune it by hand]", dropped))
	}
	out = append(out, lines...)
	return strings.Join(out, "\n")
}
func (m *Manager) Note(workspace, note string) (string, bool, error) {
	return m.notePath(m.Path(workspace), note)
}
func (m *Manager) NoteAgent(agentID, note string) (string, bool, error) {
	return m.notePath(m.AgentPath(agentID), note)
}
func (m *Manager) notePath(path, note string) (string, bool, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		return "", false, fmt.Errorf("note is empty")
	}
	if len([]rune(note)) > 300 {
		return "", false, fmt.Errorf("note too long (max 300 Unicode characters)")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return path, false, err
	}
	for _, line := range strings.Split(normalize(string(data)), "\n") {
		line = strings.TrimPrefix(strings.TrimSpace(line), "- ")
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[1]), note) {
			return path, true, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, false, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return path, false, err
	}
	_, writeErr := fmt.Fprintf(file, "- %s %s\n", time.Now().Format("2006-01-02"), note)
	closeErr := file.Close()
	if writeErr != nil {
		return path, false, writeErr
	}
	return path, false, closeErr
}
func normalize(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}
