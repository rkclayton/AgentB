package tools

import (
	"context"
	"fmt"
	"io/fs"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"

	"harness/internal/session"
)

const globMaxResults = 200

var globIgnoredDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"logs":         true,
	"memory":       true,
}

type Glob struct{}

func NewGlob() *Glob       { return &Glob{} }
func (*Glob) Name() string { return "glob" }
func (*Glob) Description() string {
	return "Find files by name pattern under a workspace path. Returns sorted relative paths; skips .git, node_modules, logs, and memory."
}
func (*Glob) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"pattern": map[string]any{"type": "string"}, "path": map[string]any{"type": "string", "default": "."}}, "required": []string{"pattern"}}
}
func (*Glob) Call(ctx context.Context, s *session.Session, args map[string]any) (string, error) {
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
	root, err := Resolve(s.Workspace, path)
	if err != nil {
		return "", err
	}
	workspaceRoot, err := Resolve(s.Workspace, ".")
	if err != nil {
		return "", err
	}
	rootRel, err := filepath.Rel(workspaceRoot, root)
	if err != nil {
		return "", err
	}
	if containsIgnoredDir(rootRel) {
		return "no matches", nil
	}

	matches := make([]string, 0, globMaxResults)
	total := 0
	err = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if filePath != root && globIgnoredDirs[entry.Name()] {
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
		rel, err := filepath.Rel(workspaceRoot, filePath)
		if err != nil {
			return err
		}
		total++
		if len(matches) < globMaxResults {
			matches = append(matches, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if total == 0 {
		return "no matches", nil
	}
	sort.Strings(matches)
	result := strings.Join(matches, "\n")
	if total > len(matches) {
		result += fmt.Sprintf("\n[truncated: %d of %d paths shown]", len(matches), total)
	}
	return result, nil
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
