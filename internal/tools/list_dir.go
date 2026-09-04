package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"harness/internal/config"
	"harness/internal/session"
)

type ListDir struct {
	mu      sync.RWMutex
	cfg     config.ListDirTool
	ignored map[string]bool
}

func NewListDir(cfg config.ListDirTool) *ListDir {
	t := &ListDir{cfg: cfg, ignored: map[string]bool{"build": true}}
	for _, name := range cfg.Ignore {
		t.ignored[name] = true
	}
	return t
}
func (*ListDir) Name() string { return "list_dir" }
func (*ListDir) Description() string {
	return "List entries under local directory path to depth. Unlike find_files, it enumerates contents without a filename pattern."
}
func (*ListDir) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "default": "."}, "depth": map[string]any{"type": "integer", "default": 1, "maximum": 3}}}
}
func (t *ListDir) Call(ctx context.Context, s *session.Session, args map[string]any) (string, error) {
	cfg, ignored := t.config()
	path, _ := args["path"].(string)
	if path == "" {
		path = "."
	}
	depth := number(args["depth"], 1)
	if depth < 1 || depth > 3 {
		return "", fmt.Errorf("depth must be between 1 and 3")
	}
	root, err := resolveForTool(ctx, s.Workspace, path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", path)
	}
	all := []string{}
	var walk func(string, int) error
	walk = func(dir string, level int) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].IsDir() != entries[j].IsDir() {
				return entries[i].IsDir()
			}
			return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
		})
		for _, entry := range entries {
			if entry.IsDir() && ignored[entry.Name()] {
				continue
			}
			prefix := strings.Repeat("  ", level-1)
			name := entry.Name()
			if entry.IsDir() {
				name += "/"
			}
			all = append(all, prefix+name)
			if entry.IsDir() && level < depth {
				if err := walk(filepath.Join(dir, entry.Name()), level+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, 1); err != nil {
		return "", err
	}
	if len(all) > cfg.MaxEntries {
		left := len(all) - cfg.MaxEntries
		all = append(all[:cfg.MaxEntries], fmt.Sprintf("[… %d more entries]", left))
	}
	s.Touch(path)
	if len(all) == 0 {
		return "directory is empty", nil
	}
	return strings.Join(all, "\n"), nil
}
func (t *ListDir) Configure(value config.Config) {
	t.mu.Lock()
	t.cfg = value.Tools.ListDir
	t.ignored = map[string]bool{"build": true}
	for _, name := range t.cfg.Ignore {
		t.ignored[name] = true
	}
	t.mu.Unlock()
}
func (t *ListDir) config() (config.ListDirTool, map[string]bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ignored := make(map[string]bool, len(t.ignored))
	for name, value := range t.ignored {
		ignored[name] = value
	}
	return t.cfg, ignored
}
