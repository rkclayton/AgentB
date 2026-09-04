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
func (m *Manager) Path(workspace string) string {
	abs, _ := filepath.Abs(workspace)
	sum := sha256.Sum256([]byte(filepath.Clean(abs)))
	name := filepath.Base(abs) + "-" + hex.EncodeToString(sum[:4]) + ".md"
	dir := m.cfg().Memory.Dir
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(m.baseDir, dir)
	}
	return filepath.Join(dir, name)
}
func (m *Manager) Load(ctx context.Context, workspace, serverID string) (string, string, error) {
	path := m.Path(workspace)
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
		body := memoryBlock(lines, dropped)
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
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := os.ReadFile(m.Path(workspace))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimRight(normalize(string(data)), "\n"), nil
}

func memoryBlock(lines []string, dropped int) string {
	if len(lines) == 0 {
		return ""
	}
	out := []string{"Notes from earlier sessions in this workspace:"}
	if dropped > 0 {
		out = append(out, fmt.Sprintf("[%d older notes omitted; the file is over the memory budget — prune it by hand]", dropped))
	}
	out = append(out, lines...)
	return strings.Join(out, "\n")
}
func (m *Manager) Note(workspace, note string) (string, bool, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		return "", false, fmt.Errorf("note is empty")
	}
	if len([]rune(note)) > 300 {
		return "", false, fmt.Errorf("note too long (max 300 chars)")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	path := m.Path(workspace)
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
