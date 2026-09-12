package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLaunchersAreHiddenByDefaultWithConsoleOptIn(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	checks := map[string][]string{
		"start-Agent_b.cmd": {"AGENTB_HIDDEN_REENTRY", "launch-hidden.vbs", "-Console"},
		filepath.Join("scripts", "launch-installed.cmd"): {"AGENTB_HIDDEN_REENTRY", `%~dp0scripts\launch-hidden.vbs`, "-Console"},
		filepath.Join("scripts", "launch-Agent_b.ps1"):   {"[switch]$Console", "$start.WindowStyle = 'Hidden'", "$start.NoNewWindow = $true", "$process.WaitForExit()", "launcher-errors.log"},
	}
	for relative, wanted := range checks {
		body, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		for _, value := range wanted {
			if !strings.Contains(text, value) {
				t.Errorf("%s does not preserve %q", relative, value)
			}
		}
	}
}

func TestAbsentBaselineRecoveryStaysOneShotAndEvidenceFirst(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	checks := map[string][]string{
		"AGENTS.md":                           {"preserve and inspect diagnostics before starting anything", "permit exactly one start", "never start it a second time", "successful start does not explain"},
		filepath.Join("docs", "HARDENING.md"): {"Application Error / Windows Error Reporting", "routine but unexplained absence permits one start", "never start it again automatically", "never applies to alpha"},
	}
	for relative, wanted := range checks {
		body, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		for _, value := range wanted {
			if !strings.Contains(text, value) {
				t.Errorf("%s does not preserve %q", relative, value)
			}
		}
	}
}
