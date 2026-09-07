package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstructionsAncestorOrderFallbackCapAndLazySubdir(t *testing.T) {
	root := t.TempDir()
	bound := filepath.Join(root, "repo")
	sub := filepath.Join(bound, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, text string) {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "AGENTS.md"), "root rule")
	write(filepath.Join(bound, "AGENTS.md"), strings.Repeat("x", InstructionLimit+20))
	write(filepath.Join(bound, "CLAUDE.md"), "ignored fallback")
	write(filepath.Join(sub, "CLAUDE.md"), "lazy rule")

	initial, err := LoadInstructions(bound, "")
	if err != nil {
		t.Fatal(err)
	}
	rootAt, repoAt := strings.Index(initial.Block, filepath.Join(root, "AGENTS.md")), strings.Index(initial.Block, filepath.Join(bound, "AGENTS.md"))
	if rootAt < 0 || repoAt <= rootAt {
		t.Fatalf("ancestor order: %q", initial.Block[:min(len(initial.Block), 200)])
	}
	if strings.Contains(initial.Block, "ignored fallback") || len(initial.Notes) != 2 {
		t.Fatalf("fallback/cap notes: %+v", initial.Notes)
	}
	if !strings.Contains(strings.Join(initial.Notes, "\n"), "truncated at 16384 bytes") {
		t.Fatalf("cap note: %+v", initial.Notes)
	}

	lazy, err := LoadInstructions(bound, sub)
	if err != nil {
		t.Fatal(err)
	}
	if len(lazy.Files) != 1 || !strings.HasSuffix(lazy.Files[0], filepath.Join("src", "CLAUDE.md")) || !strings.Contains(lazy.Block, "lazy rule") {
		t.Fatalf("lazy=%+v", lazy)
	}
}

func TestPolicyTOFUChangeRevokeUnknownAndForbiddenKeys(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".agentb"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".agentb", "policy.json")
	write := func(text string) {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager := New(data, func(string) string { return filepath.Join(data, "memory.md") })
	if manager.path != filepath.Join(data, "workspace-state.json") {
		t.Fatalf("policy trust state escaped memory root: %q", manager.path)
	}
	write(`{"version":1,"approval_mode":"mutating","shell":{"run_grant_defaults":["node --test"]}}`)
	first, err := manager.Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Policy.Approved || first.Policy.Hash == "" {
		t.Fatalf("first=%+v", first.Policy)
	}
	approved, err := manager.Approve(root, first.Policy.Hash)
	if err != nil || !approved.Approved {
		t.Fatalf("approved=%+v err=%v", approved, err)
	}
	again, err := manager.Inspect(root)
	if err != nil || !again.Policy.Approved {
		t.Fatalf("again=%+v err=%v", again.Policy, err)
	}
	write(`{"version":1,"approval_mode":"all"}`)
	changed, err := manager.Inspect(root)
	if err != nil || !changed.Policy.Changed || changed.Policy.Diff == "" {
		t.Fatalf("changed=%+v err=%v", changed.Policy, err)
	}
	if err := manager.Revoke(root); err != nil {
		t.Fatal(err)
	}
	revoked, _ := manager.Inspect(root)
	if revoked.Policy.Approved || revoked.Policy.Changed {
		t.Fatalf("revoked=%+v", revoked.Policy)
	}
	if _, err := ParsePolicy([]byte(`{"version":1,"mystery":true}`)); err == nil || !strings.Contains(err.Error(), "mystery") {
		t.Fatalf("unknown=%v", err)
	}
	if _, err := ParsePolicy([]byte(`{"version":1,"fetch":{"allow_internal_hosts":["x"]}}`)); err == nil || !strings.Contains(err.Error(), "allow_internal_hosts") {
		t.Fatalf("forbidden=%v", err)
	}
}

func TestForbiddenPolicyCapabilitiesStayOperatorFacingAndComplete(t *testing.T) {
	want := []string{"operator mode", "elevation", "signing", "allow_internal_hosts", "services allowlist"}
	if strings.Join(ForbiddenPolicyCapabilities, "|") != strings.Join(want, "|") {
		t.Fatalf("forbidden capabilities=%q", ForbiddenPolicyCapabilities)
	}
}
