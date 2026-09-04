package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/config"
)

func TestReadServingFactsCompleteness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SERVING.md")
	if facts := readServingFacts(path); facts.Complete {
		t.Fatal("missing file reported complete")
	}
	if err := os.WriteFile(path, []byte("tokenize_idle_ms=8\ntokenize_blocks_on_slot=no\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if facts := readServingFacts(path); facts.Complete {
		t.Fatal("partial file reported complete")
	}
	if err := os.WriteFile(path, []byte("tokenize_idle_ms=8\ntokenize_busy_ms=9\ntokenize_blocks_on_slot=no\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	facts := readServingFacts(path)
	if !facts.Complete || facts.TokenizeIdleMS != 8 || facts.TokenizeBusyMS != 9 || facts.TokenizeBlocksOnSlot != "no" {
		t.Fatalf("unexpected facts: %+v", facts)
	}
}

func TestStartupServerPrefersFirstRunnableProfile(t *testing.T) {
	profiles := []config.Profile{{ID: "local"}, {ID: "homepc"}, {ID: "backup"}}
	checks := []string{}
	got := startupServerID(profiles, func(id string) (bool, string) {
		checks = append(checks, id)
		return id == "homepc", "not ready"
	})
	if got != "homepc" || strings.Join(checks, ",") != "local,homepc" {
		t.Fatalf("startupServerID=%q checks=%v", got, checks)
	}
	if got := startupServerID(profiles, func(string) (bool, string) { return false, "not ready" }); got != "local" {
		t.Fatalf("fallback startupServerID=%q", got)
	}
}

func TestStartupElevationGuard(t *testing.T) {
	if err := startupElevationError(false); err != nil {
		t.Fatalf("standard token refused: %v", err)
	}
	err := startupElevationError(true)
	if err == nil || !strings.Contains(err.Error(), "refuses to run") || !strings.Contains(err.Error(), "File Explorer") {
		t.Fatalf("elevated token error = %v", err)
	}
}

func TestResolveStartupPathsPrecedence(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	localBase := t.TempDir()
	t.Setenv("LOCALAPPDATA", localBase)
	installedConfig := filepath.Join(localBase, "Agent_b", "harness.json")
	if err := os.MkdirAll(filepath.Dir(installedConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installedConfig, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	environmentConfig := filepath.Join(t.TempDir(), "environment.json")
	explicitConfig := filepath.Join(t.TempDir(), "explicit.json")
	t.Setenv("AGENTB_CONFIG", environmentConfig)

	paths, err := resolveStartupPaths(explicitConfig, filepath.Join(cwd, "application"), filepath.Join(cwd, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if paths.Config != explicitConfig || paths.Data != filepath.Join(cwd, "data") || paths.Application != filepath.Join(cwd, "application") {
		t.Fatalf("explicit paths = %+v", paths)
	}

	paths, err = resolveStartupPaths("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if paths.Config != environmentConfig || paths.Data != filepath.Join(localBase, "Agent_b") {
		t.Fatalf("environment paths = %+v", paths)
	}

	t.Setenv("AGENTB_CONFIG", "")
	paths, err = resolveStartupPaths("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if paths.Config != installedConfig || paths.Data != filepath.Join(localBase, "Agent_b") {
		t.Fatalf("installed paths = %+v", paths)
	}

	if err := os.Remove(installedConfig); err != nil {
		t.Fatal(err)
	}
	paths, err = resolveStartupPaths("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if paths.Config != filepath.Join(cwd, "harness.json") || paths.Data != cwd || paths.Application != cwd {
		t.Fatalf("development paths = %+v", paths)
	}
}
