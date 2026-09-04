package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"harness/internal/config"
	"harness/internal/session"
)

type Grep struct {
	mu     sync.RWMutex
	cfg    config.GrepTool
	ignore map[string]bool
}

func NewGrep(cfg config.GrepTool, list config.ListDirTool) *Grep {
	ignore := make(map[string]bool, len(list.Ignore))
	for _, name := range list.Ignore {
		ignore[name] = true
	}
	return &Grep{cfg: cfg, ignore: ignore}
}
func (*Grep) Name() string { return "grep" }
func (*Grep) Description() string {
	return "Search files under path for a regular expression. Returns up to 50 matching lines as file:line: text."
}
func (*Grep) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"pattern": map[string]any{"type": "string"}, "path": map[string]any{"type": "string", "default": "."}, "glob": map[string]any{"type": "string"}}, "required": []string{"pattern"}}
}
func (g *Grep) Call(ctx context.Context, s *session.Session, args map[string]any) (string, error) {
	cfg, ignore := g.config()
	pattern, ok := args["pattern"].(string)
	if !ok || pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid pattern: %v", err)
	}
	path, _ := args["path"].(string)
	if path == "" {
		path = "."
	}
	glob, _ := args["glob"].(string)
	if glob != "" {
		if _, err := filepath.Match(glob, "probe"); err != nil {
			return "", fmt.Errorf("invalid glob: %v", err)
		}
	}
	root, err := resolveForTool(ctx, s.Workspace, path)
	if err != nil {
		return "", err
	}
	matches := make([]string, 0, cfg.MaxMatches)
	total := 0
	err = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if filePath != root && ignore[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if glob != "" {
			matched, _ := filepath.Match(glob, entry.Name())
			if !matched {
				return nil
			}
		}
		data, readErr := os.ReadFile(filePath)
		if readErr != nil {
			if errors.Is(readErr, os.ErrPermission) {
				return readErr
			}
			return nil
		}
		sample := data
		if len(sample) > 8192 {
			sample = sample[:8192]
		}
		if strings.IndexByte(string(sample), 0) >= 0 {
			return nil
		}
		rel := workspaceRel(s.Workspace, filePath)
		for index, line := range strings.Split(normalizeLF(string(data)), "\n") {
			if !re.MatchString(line) {
				continue
			}
			total++
			if len(matches) >= cfg.MaxMatches {
				continue
			}
			runes := []rune(line)
			if len(runes) > cfg.MaxLineChars {
				line = string(runes[:cfg.MaxLineChars]) + "…"
			}
			matches = append(matches, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), index+1, line))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if total == 0 {
		return "no matches", nil
	}
	result := strings.Join(matches, "\n")
	if total > len(matches) {
		result += fmt.Sprintf("\n[%d of %d matches shown]", len(matches), total)
	}
	return result, nil
}
func (g *Grep) Configure(value config.Config) {
	g.mu.Lock()
	g.cfg = value.Tools.Grep
	g.ignore = make(map[string]bool, len(value.Tools.ListDir.Ignore))
	for _, name := range value.Tools.ListDir.Ignore {
		g.ignore[name] = true
	}
	g.mu.Unlock()
}
func (g *Grep) config() (config.GrepTool, map[string]bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	ignore := make(map[string]bool, len(g.ignore))
	for name, value := range g.ignore {
		ignore[name] = value
	}
	return g.cfg, ignore
}
