package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/credential"
	"harness/internal/session"
)

type missingShellCredential struct{}

func (missingShellCredential) Read() ([]byte, error) { return nil, credential.ErrNotStored }

type presentShellCredential struct{}

func (presentShellCredential) Read() ([]byte, error) { return []byte{1, 2, 3}, nil }

type completedShellProcess struct{}

func (completedShellProcess) Wait() (int, error) { return 0, nil }
func (completedShellProcess) KillTree()          {}

type exitedShellProcess struct{ code int }

func (p exitedShellProcess) Wait() (int, error) { return p.code, nil }
func (exitedShellProcess) KillTree()            {}

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

func TestShellServiceAccountSpawnFailureRequiresOperatorApproval(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults(root).Shell
	cfg.ServiceAccount.Enabled = true
	shell := NewShell(cfg)
	shell.Configure(config.Config{Workspace: root, Shell: cfg})
	shell.SetCredentialStore(presentShellCredential{})
	shell.startService = func(string, []string, string, []string, config.ShellServiceAccount, []byte, *lockedBuffer) (runningShellProcess, error) {
		return nil, &serviceSpawnError{kind: "service-account authentication failed"}
	}
	var reported ShellIdentityStatus
	shell.SetIdentityReporter(func(status ShellIdentityStatus) { reported = status })
	command := "printf must-not-run"
	if runtime.GOOS == "windows" {
		command = "Write-Output must-not-run"
	}
	detail := shell.CallDetailed(context.Background(), &session.Session{ID: "test", Workspace: root}, map[string]any{"command": command})
	if detail.Err != nil || detail.OperatorOverrideReason == "" || strings.Contains(detail.Content, "must-not-run") {
		t.Fatalf("detail=%+v, want an unexecuted operator-approval request", detail)
	}
	if reported.Fallback || !reported.OperatorApprovalRequired || !strings.Contains(reported.Reason, "authentication failed") || reported.Since == "" {
		t.Fatalf("identity report = %+v", reported)
	}
	if _, err := time.Parse(time.RFC3339, reported.Since); err != nil {
		t.Fatalf("alarm timestamp = %q: %v", reported.Since, err)
	}
}

func TestShellServiceAccountDisabledDoesNotRaiseIdentityAlarm(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults(root).Shell
	shell := NewShell(cfg)
	called := false
	shell.SetIdentityReporter(func(status ShellIdentityStatus) { called = true })
	command := "printf disabled-ok"
	if runtime.GOOS == "windows" {
		command = "Write-Output disabled-ok"
	}
	result, err := shell.Call(context.Background(), &session.Session{ID: "test", Workspace: root}, map[string]any{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "disabled-ok") || called || shell.IdentityStatus().Fallback || shell.IdentityStatus().OperatorApprovalRequired {
		t.Fatalf("result=%q called=%v status=%+v", result, called, shell.IdentityStatus())
	}
}

func TestShellServiceAccountSuccessClearsPersistentIdentityAlarm(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults(root).Shell
	cfg.ServiceAccount.Enabled = true
	shell := NewShell(cfg)
	shell.Configure(config.Config{Workspace: root, Shell: cfg})
	shell.SetCredentialStore(presentShellCredential{})
	shell.identity = ShellIdentityStatus{OperatorApprovalRequired: true, Reason: "earlier failure", Since: time.Now().UTC().Format(time.RFC3339)}
	shell.startService = func(string, []string, string, []string, config.ShellServiceAccount, []byte, *lockedBuffer) (runningShellProcess, error) {
		return completedShellProcess{}, nil
	}
	message, err := shell.TestServiceAccount(context.Background())
	if err != nil || !strings.Contains(message, "succeeded") {
		t.Fatalf("message=%q err=%v", message, err)
	}
	if status := shell.IdentityStatus(); status.Fallback || status.OperatorApprovalRequired || status.Reason != "" || status.Since != "" {
		t.Fatalf("identity status was not cleared: %+v", status)
	}
}

func TestShellServiceAccountTestFailureRaisesPersistentIdentityAlarm(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults(root).Shell
	cfg.ServiceAccount.Enabled = true
	shell := NewShell(cfg)
	shell.Configure(config.Config{Workspace: root, Shell: cfg})
	shell.SetCredentialStore(presentShellCredential{})
	shell.startService = func(string, []string, string, []string, config.ShellServiceAccount, []byte, *lockedBuffer) (runningShellProcess, error) {
		return nil, &serviceSpawnError{kind: "service account cannot access the configured workspace"}
	}
	message, err := shell.TestServiceAccount(context.Background())
	status := shell.IdentityStatus()
	if err == nil || !strings.Contains(message, "workspace") || !status.OperatorApprovalRequired || status.Reason != message || status.Since == "" {
		t.Fatalf("message=%q err=%v status=%+v", message, err, status)
	}
}

func TestShellServiceAccountTestResolvesRelativeWorkspace(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	cfg := config.Defaults(workspace).Shell
	cfg.ServiceAccount.Enabled = true
	shell := NewShell(cfg)
	shell.Configure(config.Config{Workspace: "./workspace", Shell: cfg})
	shell.SetCredentialStore(presentShellCredential{})
	var received string
	shell.startService = func(_ string, _ []string, got string, _ []string, _ config.ShellServiceAccount, _ []byte, _ *lockedBuffer) (runningShellProcess, error) {
		received = got
		return completedShellProcess{}, nil
	}
	message, err := shell.TestServiceAccount(context.Background())
	if err != nil || !strings.Contains(message, "succeeded") {
		t.Fatalf("message=%q err=%v", message, err)
	}
	if received != workspace || !filepath.IsAbs(received) {
		t.Fatalf("service-account working directory = %q, want absolute %q", received, workspace)
	}
}

func TestShellServicePermissionDenialOffersOperatorOverride(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults(root).Shell
	cfg.ServiceAccount.Enabled = true
	shell := NewShell(cfg)
	shell.Configure(config.Config{Workspace: root, Shell: cfg})
	shell.SetCredentialStore(presentShellCredential{})
	shell.startService = func(_ string, _ []string, _ string, _ []string, _ config.ShellServiceAccount, _ []byte, output *lockedBuffer) (runningShellProcess, error) {
		_, _ = output.Write([]byte("Set-Content: Access to the path is denied.\nUnauthorizedAccessException\n"))
		return exitedShellProcess{code: 1}, nil
	}
	detail := shell.CallDetailed(context.Background(), &session.Session{ID: "test", Workspace: root}, map[string]any{"command": "Set-Content protected.txt value"})
	if detail.Err != nil {
		t.Fatal(detail.Err)
	}
	if detail.OperatorOverrideReason == "" || !strings.Contains(detail.Content, "exit=1") {
		t.Fatalf("detail=%+v, want permission override", detail)
	}
}

func TestShellSpawnFailureDoesNotExecuteOperatorCommand(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults(root).Shell
	cfg.ServiceAccount.Enabled = true
	shell := NewShell(cfg)
	shell.Configure(config.Config{Workspace: root, Shell: cfg})
	shell.SetCredentialStore(presentShellCredential{})
	shell.startService = func(string, []string, string, []string, config.ShellServiceAccount, []byte, *lockedBuffer) (runningShellProcess, error) {
		return nil, &serviceSpawnError{kind: "service-account authentication failed"}
	}
	marker := filepath.Join(root, "must-not-exist")
	command := "touch must-not-exist"
	if runtime.GOOS == "windows" {
		command = "New-Item must-not-exist"
	}
	detail := shell.CallDetailed(context.Background(), &session.Session{ID: "test", Workspace: root}, map[string]any{"command": command})
	if detail.Err != nil || detail.OperatorOverrideReason == "" {
		t.Fatalf("detail=%+v", detail)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("operator command ran without approval: %v", err)
	}
}

func TestShellOperatorOverrideBypassesServiceSpawner(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults(root).Shell
	cfg.ServiceAccount.Enabled = true
	shell := NewShell(cfg)
	shell.Configure(config.Config{Workspace: root, Shell: cfg})
	shell.startService = func(string, []string, string, []string, config.ShellServiceAccount, []byte, *lockedBuffer) (runningShellProcess, error) {
		t.Fatal("operator override called service-account process starter")
		return nil, nil
	}
	command := "printf operator-ok"
	if runtime.GOOS == "windows" {
		command = "Write-Output operator-ok"
	}
	result, err := shell.CallAsOperator(context.Background(), &session.Session{ID: "test", Workspace: root}, map[string]any{"command": command})
	if err != nil || !strings.Contains(result, "operator-ok") {
		t.Fatalf("result=%q err=%v", result, err)
	}
}

func TestPermissionDeniedOutput(t *testing.T) {
	for _, value := range []string{
		"Access is denied.",
		"Access to the path 'x' is denied.",
		"permission denied",
		"CategoryInfo: PermissionDenied: (:) [], UnauthorizedAccessException",
		"The requested operation requires elevation.",
		"operation not permitted",
	} {
		if !permissionDeniedOutput(value) {
			t.Errorf("did not recognize %q", value)
		}
	}
	if permissionDeniedOutput("the file is locked by another process") {
		t.Fatal("ordinary command failure was classified as a permission denial")
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
