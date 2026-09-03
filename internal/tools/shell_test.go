package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/session"
)

func TestShellFileRoutingRefusals(t *testing.T) {
	discovery := []string{
		"ls .", "dir .", "Get-ChildItem .", "gci .", `find . -name "*.go"`,
		"tree .", "where *.go", "Where-Object .", "fd *.go .",
		"Get-ChildItem -Path . -Filter *.go -Recurse -File | Select-Object -ExpandProperty FullName",
	}
	reads := []string{
		"cat README.md", "type README.md", "Get-Content README.md", "gc README.md",
		"head README.md", "tail README.md", "more README.md", "less README.md",
	}
	for _, test := range []struct {
		name, tool string
		commands   []string
	}{{"discovery", "glob", discovery}, {"read", "read_file", reads}} {
		t.Run(test.name, func(t *testing.T) {
			for _, command := range test.commands {
				t.Run(strings.Fields(command)[0], func(t *testing.T) {
					cfg := config.Defaults(t.TempDir()).Shell
					got, err := NewShell(cfg).Call(context.Background(), &session.Session{ID: "test", Workspace: t.TempDir()}, map[string]any{"command": command})
					if err != nil {
						t.Fatal(err)
					}
					var refusal shellRoutingRefusal
					if err := json.Unmarshal([]byte(got), &refusal); err != nil {
						t.Fatalf("result is not structured JSON: %q: %v", got, err)
					}
					if !refusal.Refused || refusal.Replacement.Tool != test.tool {
						t.Fatalf("got %+v, want refusal naming %s", refusal, test.tool)
					}
					if len(refusal.Replacement.Arguments) == 0 {
						t.Fatal("refusal omitted replacement arguments")
					}
				})
			}
		})
	}
}

func TestShellFileRoutingArguments(t *testing.T) {
	discovery, _ := inspectShellFileRouting(`Get-ChildItem -Path src -Filter "*.go" -Recurse`)
	if discovery == nil || discovery.Replacement.Tool != "glob" || discovery.Replacement.Arguments["pattern"] != "*.go" || discovery.Replacement.Arguments["path"] != "src" {
		t.Fatalf("unexpected discovery replacement: %+v", discovery)
	}
	read, _ := inspectShellFileRouting(`Get-Content -Path "docs/guide.md"`)
	if read == nil || read.Replacement.Tool != "read_file" || read.Replacement.Arguments["path"] != "docs/guide.md" {
		t.Fatalf("unexpected read replacement: %+v", read)
	}
}

func TestShellFileRoutingCompoundAllowed(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "route.txt"), []byte("ROUTE_OK\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults(root).Shell
	command := "find . | wc -l"
	if runtime.GOOS == "windows" {
		command = "Get-ChildItem . | ForEach-Object { $_.Name }"
	}
	got, err := NewShell(cfg).Call(context.Background(), &session.Session{ID: "test", Workspace: root}, map[string]any{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, `"refused":true`) {
		t.Fatalf("compound command was refused: %s", got)
	}
}

func TestShellFileRoutingDisabled(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "route.txt"), []byte("ROUTE_OK\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults(root).Shell
	disabled := false
	cfg.FileRoutingGuard = &disabled
	command := "cat route.txt"
	if runtime.GOOS == "windows" {
		command = "Get-Content route.txt"
	}
	got, err := NewShell(cfg).Call(context.Background(), &session.Session{ID: "test", Workspace: root}, map[string]any{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "ROUTE_OK") {
		t.Fatalf("disabled guard did not execute command: %q", got)
	}
}
