package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"harness/internal/config"
	"harness/internal/session"
)

type ListDir struct {
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
	return "List a workspace directory, one entry per line, directories end with /. Skips .git and build folders."
}
func (*ListDir) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "default": "."}, "depth": map[string]any{"type": "integer", "default": 1, "maximum": 3}}}
}
func (t *ListDir) Call(ctx context.Context, s *session.Session, args map[string]any) (string, error) {
	path, _ := args["path"].(string)
	if path == "" {
		path = "."
	}
	depth := number(args["depth"], 1)
	if depth < 1 || depth > 3 {
		return "", fmt.Errorf("depth must be between 1 and 3")
	}
	root, err := Resolve(s.Workspace, path)
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
			if entry.IsDir() && t.ignored[entry.Name()] {
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
	if len(all) > t.cfg.MaxEntries {
		left := len(all) - t.cfg.MaxEntries
		all = append(all[:t.cfg.MaxEntries], fmt.Sprintf("[… %d more entries]", left))
	}
	s.Touch(path)
	return strings.Join(all, "\n"), nil
}
