//go:build windows

package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/credential"
	"harness/internal/events"
	"harness/internal/session"
)

// TestCapabilitySuiteLiveServiceSplit is the fixed 2l-iv host suite. It is
// opt-in because it uses the installed service identity and public network.
func TestCapabilitySuiteLiveServiceSplit(t *testing.T) {
	if os.Getenv("AGENTB_CAPABILITY_LIVE") != "1" {
		t.Skip("set AGENTB_CAPABILITY_LIVE=1 with approved disposable roots")
	}
	workspaceParent, dataRoot, appRoot := os.Getenv("AGENTB_CAPABILITY_WORKSPACE"), os.Getenv("AGENTB_CAPABILITY_DATA"), os.Getenv("AGENTB_CAPABILITY_APP")
	if workspaceParent == "" || dataRoot == "" || appRoot == "" {
		t.Fatal("AGENTB_CAPABILITY_WORKSPACE, _DATA, and _APP are required")
	}
	workspace, err := os.MkdirTemp(workspaceParent, "agentb-capability-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := removeCapabilityFixture(workspace); err != nil {
			t.Errorf("remove capability fixture: %v", err)
		}
	})
	cfg := config.Defaults(workspace)
	cfg.Shell.ServiceAccount.Enabled = true
	guard := false
	cfg.Shell.FileRoutingGuard = &guard
	store := credential.New(dataRoot)
	shell := NewShell(cfg.Shell)
	shell.Configure(cfg)
	shell.SetCredentialStore(store)
	coordinator := NewFileCoordinator(session.NewWorkspaceRegistry(), func(string) string { return "capability" }, events.NewBus())
	shell.SetFileCoordinator(coordinator)
	fileIdentity := NewFileIdentity(store)
	fileIdentity.Configure(cfg)
	whoami, err := exec.LookPath("whoami.exe")
	if err != nil {
		t.Fatal(err)
	}
	identityOutput, err := exec.Command(whoami).Output()
	if err != nil {
		t.Fatal(err)
	}
	operatorIdentity := strings.TrimSpace(string(identityOutput))
	if operatorIdentity == "" || strings.EqualFold(filepath.Base(operatorIdentity), cfg.Shell.ServiceAccount.Account) || strings.HasSuffix(strings.ToLower(operatorIdentity), `\`+strings.ToLower(cfg.Shell.ServiceAccount.Account)) {
		t.Fatalf("operator identity evidence is not distinct from service account: %q", operatorIdentity)
	}
	serviceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/identity" || r.Header.Get("Authorization") != "Bearer "+operatorIdentity {
			http.Error(w, `{"title":"identity mismatch"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"identity_match":true}`))
	}))
	defer serviceServer.Close()
	cfg.Services = map[string]config.Service{"identity": {
		BaseURL: serviceServer.URL + "/api", Auth: "exec:" + whoami,
		AllowedMethods: []string{"GET"}, TimeoutS: 10, MaxBodyKB: 16,
	}}
	callService := NewCallService(cfg.Services)
	toolRegistry := New(
		fileIdentity.Wrap(NewReadFile(cfg.Tools.ReadFile)), fileIdentity.Wrap(NewListDir(cfg.Tools.ListDir)),
		fileIdentity.Wrap(NewWriteFile(coordinator)), fileIdentity.Wrap(NewEditFile(coordinator)),
		fileIdentity.Wrap(NewGrep(cfg.Tools.Grep, cfg.Tools.ListDir)), NewFetch(cfg.Tools.Fetch), fileIdentity.Wrap(NewGlob(cfg.Tools.FindFiles)), callService,
	)
	enabled := map[string]bool{"read_file": true, "list_dir": true, "write_file": true, "edit_file": true, "search_text": true, "fetch_url": true, "find_files": true, "call_service": true}
	item := &session.Session{ID: "capability", Workspace: workspace, LastSeen: map[string]time.Time{}, ToolsEnabled: enabled}

	t.Run("write_and_run_python_node_shell", func(t *testing.T) {
		cases := []struct{ name, file, body, want string }{
			{"python", "capability.py", "print('python-capability')", "python-capability"},
			{"node", "capability.js", "console.log('node-capability')", "node-capability"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if result := toolRegistry.CallDetailed(context.Background(), item, "write_file", map[string]any{"path": tc.file, "content": tc.body}); !result.OK {
					t.Fatalf("write=%+v", result)
				}
				interpreter, err := exec.LookPath(tc.name)
				if err != nil {
					t.Skipf("%s not installed", tc.name)
				}
				command := "& " + quotePowerShell(interpreter) + " " + quotePowerShell(tc.file)
				detail := shell.CallDetailed(context.Background(), item, map[string]any{"command": command})
				if detail.OperatorOverrideReason != "" {
					output, overrideErr := shell.CallAsOperator(context.Background(), item, map[string]any{"command": command})
					if overrideErr != nil || !strings.Contains(output, tc.want) {
						t.Fatalf("service=%+v override=%q err=%v", detail, output, overrideErr)
					}
					t.Logf("moved behind operator decision: %s", detail.OperatorOverrideReason)
					return
				}
				if detail.Err != nil || !strings.Contains(detail.Content, tc.want) {
					t.Fatalf("detail=%+v", detail)
				}
			})
		}
	})

	t.Run("project_tests_shell", func(t *testing.T) {
		source := os.Getenv("AGENTB_CAPABILITY_SOURCE")
		if source == "" {
			t.Skip("AGENTB_CAPABILITY_SOURCE not set")
		}
		goExe := os.Getenv("AGENTB_CAPABILITY_GO")
		if goExe == "" {
			goExe = filepath.Join(source, ".tools", "go", "bin", "go.exe")
		}
		if _, err := os.Stat(goExe); err != nil {
			t.Skip(err)
		}
		command := "$env:AGENTB_CAPABILITY_LIVE=$null; Set-Location -LiteralPath " + quotePowerShell(source) + "; & " + quotePowerShell(goExe) + " test ./internal/tools"
		output, runErr := shell.CallAsOperator(context.Background(), item, map[string]any{"command": command, "timeout_s": 600})
		if runErr != nil || !strings.Contains(output, "ok  \tharness/internal/tools") {
			t.Fatalf("output=%q err=%v", output, runErr)
		}
	})

	t.Run("read_edit_search_find_file_tools", func(t *testing.T) {
		path := filepath.Join(workspace, "capability-text.txt")
		if err := os.WriteFile(path, []byte("before needle   \r\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if result := toolRegistry.CallDetailed(context.Background(), item, "read_file", map[string]any{"path": path}); !result.OK {
			t.Fatalf("read=%+v", result)
		}
		if result := toolRegistry.CallDetailed(context.Background(), item, "edit_file", map[string]any{"path": path, "old_string": "before needle\n", "new_string": "after needle\n"}); !result.OK || !strings.Contains(result.Content, "strategy: whitespace-normalized") || !strings.Contains(result.Content, "--- a/capability-text.txt") {
			t.Fatalf("edit=%+v", result)
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != "after needle\r\n" {
			t.Fatalf("edit line-ending result=%q err=%v", data, err)
		}
		if result := toolRegistry.CallDetailed(context.Background(), item, "search_text", map[string]any{"path": workspace, "pattern": "needle"}); !result.OK || !strings.Contains(result.Content, "capability-text.txt") {
			t.Fatalf("search=%+v", result)
		}
		if result := toolRegistry.CallDetailed(context.Background(), item, "find_files", map[string]any{"path": workspace, "pattern": "capability-*"}); !result.OK || !strings.Contains(result.Content, "capability-text.txt") {
			t.Fatalf("find=%+v", result)
		}
		if result := toolRegistry.CallDetailed(context.Background(), item, "list_dir", map[string]any{"path": workspace}); !result.OK || !strings.Contains(result.Content, "capability-text.txt") {
			t.Fatalf("list=%+v", result)
		}
	})

	t.Run("d_plan_file_boundary_under_service_identity", func(t *testing.T) {
		repositoryFile := filepath.Join(workspace, "d-repository-source.txt")
		if err := os.WriteFile(repositoryFile, []byte("repository evidence"), 0o600); err != nil {
			t.Fatal(err)
		}
		d := &session.Session{ID: "capability-d", Role: "d", Workspace: workspace, PlansRoot: filepath.Join(workspace, "d-plans"), LastSeen: map[string]time.Time{}, ToolsEnabled: enabled}
		if result := toolRegistry.CallDetailed(context.Background(), d, "write_file", map[string]any{"path": "plan.md", "content": "# Capability plan\n"}); !result.OK {
			t.Fatalf("d write=%+v", result)
		}
		if d.PlanID == "" || d.PlanDir == "" {
			t.Fatalf("d plan was not bound: %+v", d.Snapshot())
		}
		if result := toolRegistry.CallDetailed(context.Background(), d, "read_file", map[string]any{"path": repositoryFile}); !result.OK || !strings.Contains(result.Content, "repository evidence") {
			t.Fatalf("d repository read=%+v", result)
		}
		if result := toolRegistry.CallDetailed(context.Background(), d, "write_file", map[string]any{"path": filepath.Join(workspace, "d-escape.txt"), "content": "escape"}); result.OK || !strings.Contains(result.Content, "outside the plan") || result.OperatorOverrideReason != "" {
			t.Fatalf("d repository write did not match the direct plan-jail refusal: %+v", result)
		} else if _, ok := toolRegistry.CallAsOperator(context.Background(), d, "write_file", map[string]any{"path": filepath.Join(workspace, "d-escape.txt"), "content": "escape"}); ok {
			t.Fatal("operator identity widened the d repository-write jail")
		}
		sibling := filepath.Join(d.PlansRoot, "sibling")
		if err := os.MkdirAll(sibling, 0o700); err != nil {
			t.Fatal(err)
		}
		siblingPlan := filepath.Join(sibling, "plan.md")
		if err := os.WriteFile(siblingPlan, []byte("# Sibling plan\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if result := toolRegistry.CallDetailed(context.Background(), d, "read_file", map[string]any{"path": siblingPlan}); !result.OK || !strings.Contains(result.Content, "Sibling plan") {
			t.Fatalf("d plan-tree read did not use the union jail: %+v", result)
		}
	})

	t.Run("fetch_public_text_fetch_url", func(t *testing.T) {
		value, err := NewFetch(cfg.Tools.Fetch).Call(context.Background(), item, map[string]any{"url": "https://example.com"})
		if err != nil || !strings.Contains(value, "Example Domain") {
			t.Fatalf("fetch=%q err=%v", value, err)
		}
	})

	t.Run("internal_service_exec_identity_call_service", func(t *testing.T) {
		detail := toolRegistry.CallDetailed(context.Background(), item, "call_service", map[string]any{"service": "identity", "method": "GET", "path": "identity"})
		if !detail.OK || !detail.OperatorContext || !strings.Contains(detail.Content, `"identity_match":true`) || strings.Contains(detail.Content, operatorIdentity) {
			t.Fatalf("detail=%+v", detail)
		}
		t.Logf("call_service exec child identity: %s (service account: %s); contract=new/pass", operatorIdentity, cfg.Shell.ServiceAccount.Account)
	})

	t.Run("boundary_file_tool_operator_decision", func(t *testing.T) {
		wrapped := fileIdentity.Wrap(NewReadFile(cfg.Tools.ReadFile)).(DetailedTool)
		detail := wrapped.CallDetailed(context.Background(), item, map[string]any{"path": filepath.Join(dataRoot, "harness.json")})
		if detail.OperatorOverrideReason == "" {
			t.Fatalf("service boundary did not request operator decision: %+v", detail)
		}
	})

	t.Run("multiline_powershell_run_script", func(t *testing.T) {
		detail := NewRunScript(shell).CallDetailed(context.Background(), item, map[string]any{"language": "powershell", "source": "$sum = 0\n1..4 | ForEach-Object { $sum += $_ }\n\"sum=$sum\""})
		if detail.Err != nil || detail.OperatorOverrideReason != "" || !strings.Contains(detail.Content, "sum=10") {
			t.Fatalf("detail=%+v", detail)
		}
	})
}

func TestRemoveCapabilityFixture(t *testing.T) {
	path := os.Getenv("AGENTB_CAPABILITY_CLEANUP_WORKSPACE")
	if path == "" {
		t.Skip("set AGENTB_CAPABILITY_CLEANUP_WORKSPACE to an exact capability fixture")
	}
	if err := removeCapabilityFixture(path); err != nil {
		t.Fatal(err)
	}
}

func removeCapabilityFixture(path string) error {
	clean, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(strings.ToLower(filepath.Base(clean)), "agentb-") {
		return fmt.Errorf("refusing non-capability fixture %q", clean)
	}
	return os.RemoveAll(clean)
}
