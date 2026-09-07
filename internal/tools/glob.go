package tools

import (
	"context"
	"fmt"
	"io/fs"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"harness/internal/config"
	"harness/internal/session"
)

const globMaxResults = 200

var globIgnoredDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"logs":         true,
	"memory":       true,
}

type Glob struct {
	mu      sync.RWMutex
	cfg     config.FindFilesTool
	walkDir func(string, fs.WalkDirFunc) error
}

func NewGlob(cfg config.FindFilesTool) *Glob {
	return &Glob{cfg: cfg, walkDir: filepath.WalkDir}
}
func (*Glob) Name() string { return "find_files" }
func (*Glob) Description() string {
	return "Find local files under path whose names or relative paths match pattern. Unlike search_text, it does not inspect file contents."
}
func (*Glob) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"pattern": map[string]any{"type": "string"}, "path": map[string]any{"type": "string", "default": "."}}, "required": []string{"pattern"}}
}
func (g *Glob) Call(ctx context.Context, s *session.Session, args map[string]any) (string, error) {
	cfg := g.config()
	cfg.SkipRoots = append(cfg.SkipRoots, s.Policy().FindFiles.SkipRootsAdd...)
	pattern, ok := args["pattern"].(string)
	if !ok || pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	pattern = strings.ReplaceAll(pattern, `\`, "/")
	if err := validateGlobPattern(pattern); err != nil {
		return "", fmt.Errorf("invalid pattern: %v", err)
	}
	path, _ := args["path"].(string)
	if path == "" {
		path = "."
	}
	root, err := resolveForTool(ctx, s.Workspace, path)
	if err != nil {
		return "", err
	}
	workspaceRoot, err := resolveForTool(ctx, s.Workspace, ".")
	if err != nil {
		return "", err
	}
	rootRel := workspaceRel(workspaceRoot, root)
	if containsIgnoredDir(rootRel) {
		return "no matches", nil
	}

	matches := make([]string, 0, globMaxResults)
	total := 0
	skipped := 0
	err = g.walkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if filePath != root && isInaccessible(walkErr) {
				skipped++
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if filePath != root && globIgnoredDirs[entry.Name()] {
				return filepath.SkipDir
			}
			if filePath != root && configuredSkipRoot(filePath, cfg.SkipRoots) {
				return filepath.SkipDir
			}
			return nil
		}
		matchPath, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		matched := globMatches(pattern, filepath.ToSlash(matchPath), entry.Name())
		if !matched {
			return nil
		}
		rel := workspaceRel(workspaceRoot, filePath)
		total++
		if len(matches) < globMaxResults {
			matches = append(matches, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	s.TouchProject(path)
	if total == 0 {
		return withSkippedInaccessible("no matches", skipped), nil
	}
	sort.Strings(matches)
	result := strings.Join(matches, "\n")
	if total > len(matches) {
		result += fmt.Sprintf("\n[truncated: %d of %d paths shown]", len(matches), total)
	}
	return withSkippedInaccessible(result, skipped), nil
}

func (g *Glob) Configure(value config.Config) {
	g.mu.Lock()
	g.cfg = value.Tools.FindFiles
	g.mu.Unlock()
}

func (g *Glob) config() config.FindFilesTool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return config.FindFilesTool{SkipRoots: append([]string(nil), g.cfg.SkipRoots...)}
}

func configuredSkipRoot(filePath string, patterns []string) bool {
	volume := filepath.VolumeName(filePath)
	relative := strings.TrimLeft(filepath.Clean(strings.TrimPrefix(filePath, volume)), `\/`)
	parts := strings.Split(filepath.ToSlash(relative), "/")
	for _, raw := range patterns {
		normalized := strings.ReplaceAll(strings.TrimSpace(raw), `\`, "/")
		patternParts := strings.Split(strings.Trim(normalized, "/"), "/")
		if len(patternParts) == 0 || len(patternParts) > len(parts) {
			continue
		}
		matched := true
		for index := range patternParts {
			ok, err := pathpkg.Match(strings.ToLower(patternParts[index]), strings.ToLower(parts[index]))
			if err != nil || !ok {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func validateGlobPattern(pattern string) error {
	for _, part := range strings.Split(pattern, "/") {
		if part == "**" {
			continue
		}
		if _, err := pathpkg.Match(part, "probe"); err != nil {
			return err
		}
	}
	return nil
}

func globMatches(pattern, relativePath, name string) bool {
	if !strings.Contains(pattern, "/") {
		matched, _ := pathpkg.Match(pattern, name)
		return matched
	}
	patternParts := strings.Split(pattern, "/")
	pathParts := strings.Split(relativePath, "/")
	type position struct{ pattern, path int }
	seen := map[position]bool{}
	var match func(int, int) bool
	match = func(patternIndex, pathIndex int) bool {
		pos := position{patternIndex, pathIndex}
		if seen[pos] {
			return false
		}
		seen[pos] = true
		if patternIndex == len(patternParts) {
			return pathIndex == len(pathParts)
		}
		if patternParts[patternIndex] == "**" {
			return match(patternIndex+1, pathIndex) || pathIndex < len(pathParts) && match(patternIndex, pathIndex+1)
		}
		if pathIndex == len(pathParts) {
			return false
		}
		matched, _ := pathpkg.Match(patternParts[patternIndex], pathParts[pathIndex])
		return matched && match(patternIndex+1, pathIndex+1)
	}
	return match(0, 0)
}

func containsIgnoredDir(path string) bool {
	if path == "." || path == "" {
		return false
	}
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if globIgnoredDirs[part] {
			return true
		}
	}
	return false
}
