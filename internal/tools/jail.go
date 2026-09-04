package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func Resolve(workspace, path string) (string, error) {
	return resolvePath(workspace, path, true)
}

type osPathPolicyKey struct{}

func withOSPathPolicy(ctx context.Context) context.Context {
	return context.WithValue(ctx, osPathPolicyKey{}, true)
}

func resolveForTool(ctx context.Context, workspace, path string) (string, error) {
	enforceWorkspace := true
	if allowed, _ := ctx.Value(osPathPolicyKey{}).(bool); allowed {
		enforceWorkspace = false
	}
	return resolvePath(workspace, path, enforceWorkspace)
}

func resolvePath(workspace, path string, enforceWorkspace bool) (string, error) {
	if path == "" {
		path = "."
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	candidate := filepath.FromSlash(path)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate = filepath.Clean(candidate)
	existing := candidate
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("path is outside the workspace")
		}
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	rest, err := filepath.Rel(existing, candidate)
	if err != nil {
		return "", err
	}
	candidate = filepath.Join(resolved, rest)
	if !enforceWorkspace {
		return candidate, nil
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path is outside the workspace")
	}
	return candidate, nil
}
