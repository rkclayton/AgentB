package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"harness/internal/config"
	"harness/internal/session"
)

type Grep struct {
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
	root, err := Resolve(s.Workspace, path)
	if err != nil {
		return "", err
	}
	matches := make([]string, 0, g.cfg.MaxMatches)
	total := 0
	err = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if filePath != root && g.ignore[entry.Name()] {
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
			return nil
		}
		sample := data
		if len(sample) > 8192 {
			sample = sample[:8192]
		}
		if strings.IndexByte(string(sample), 0) >= 0 {
			return nil
		}
		rel, _ := filepath.Rel(s.Workspace, filePath)
		for index, line := range strings.Split(normalizeLF(string(data)), "\n") {
			if !re.MatchString(line) {
				continue
			}
			total++
			if len(matches) >= g.cfg.MaxMatches {
				continue
			}
			runes := []rune(line)
			if len(runes) > g.cfg.MaxLineChars {
				line = string(runes[:g.cfg.MaxLineChars]) + "…"
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
