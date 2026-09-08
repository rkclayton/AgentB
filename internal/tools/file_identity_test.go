package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/session"
)

type fileIdentityTestCredential struct {
	password []byte
	err      error
}

func (c *fileIdentityTestCredential) Read() ([]byte, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.password, nil
}

type permissionDeniedFileTool struct{}

func (*permissionDeniedFileTool) Name() string           { return "read_file" }
func (*permissionDeniedFileTool) Description() string    { return "test" }
func (*permissionDeniedFileTool) Schema() map[string]any { return map[string]any{} }
func (*permissionDeniedFileTool) Call(context.Context, *session.Session, map[string]any) (string, error) {
	return "", os.ErrPermission
}

type descriptionTestTool struct{ name string }

func (t descriptionTestTool) Name() string         { return t.name }
func (descriptionTestTool) Description() string    { return "file tool" }
func (descriptionTestTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (descriptionTestTool) Call(context.Context, *session.Session, map[string]any) (string, error) {
	return "", nil
}

func enabledFileIdentity(t *testing.T, credential *fileIdentityTestCredential) *FileIdentity {
	t.Helper()
	identity := NewFileIdentity(credential)
	identity.run = func(_ config.ShellServiceAccount, _ []byte, call func() (string, error)) (string, error) {
		return call()
	}
	cfg := config.Defaults(t.TempDir())
	cfg.Shell.ServiceAccount.Enabled = true
	cfg.Shell.ServiceAccount.Account = "agentb-test"
	cfg.Shell.ServiceAccount.Domain = "."
	identity.Configure(cfg)
	return identity
}

func TestFileIdentityKeepsBoundDirectoryJailUnderServiceIdentity(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	external := filepath.Join(root, "external")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "visible.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	credential := &fileIdentityTestCredential{password: []byte{1, 2, 3}}
	identity := enabledFileIdentity(t, credential)
	tool := identity.Wrap(NewListDir(config.Defaults(workspace).Tools.ListDir))
	result, err := tool.Call(context.Background(), &session.Session{Workspace: workspace, LastSeen: map[string]time.Time{}}, map[string]any{"path": external})
	if err != nil || !strings.Contains(result, "bound-directory jail") {
		t.Fatalf("result=%q err=%v", result, err)
	}
	if strings.Trim(string(credential.password), "\x00") != "" {
		t.Fatal("credential bytes were not cleared after use")
	}
}

func TestFileIdentityDisabledKeepsWorkspaceBoundary(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	external := filepath.Join(root, "external")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}
	identity := NewFileIdentity(nil)
	identity.Configure(config.Defaults(workspace))
	_, err := identity.Wrap(NewListDir(config.Defaults(workspace).Tools.ListDir)).Call(
		context.Background(), &session.Session{Workspace: workspace}, map[string]any{"path": external},
	)
	if err == nil || !strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("err=%v", err)
	}
}

func TestFileIdentityOperatorContextUsesOperatorOSAccessOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	external := filepath.Join(root, "external")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "operator-visible.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	identity := NewFileIdentity(nil)
	cfg := config.Defaults(workspace)
	cfg.Shell.OperatorContext = true
	cfg.Shell.ServiceAccount.Enabled = true
	identity.Configure(cfg)
	detail := identity.Wrap(NewListDir(cfg.Tools.ListDir)).(DetailedTool).CallDetailed(
		context.Background(), &session.Session{Workspace: workspace, LastSeen: map[string]time.Time{}}, map[string]any{"path": external},
	)
	if detail.Err != nil || !detail.OperatorContext || !strings.Contains(detail.Content, "operator-visible.txt") {
		t.Fatalf("detail=%+v", detail)
	}
}

func TestFileIdentityPermissionDenialOffersOperatorOverride(t *testing.T) {
	identity := enabledFileIdentity(t, &fileIdentityTestCredential{password: []byte{1, 2, 3}})
	detail := identity.Wrap(&permissionDeniedFileTool{}).(DetailedTool).CallDetailed(
		context.Background(), &session.Session{Workspace: t.TempDir()}, map[string]any{"path": `C:\protected.txt`},
	)
	if detail.Err != nil || detail.OperatorOverrideReason != "service account was denied permission for the requested path" {
		t.Fatalf("detail=%+v", detail)
	}
}

func TestFileToolJailDescriptionOnlyWhenServiceSplitEnabled(t *testing.T) {
	workspace := t.TempDir()
	identity := NewFileIdentity(nil)
	cfg := config.Defaults(workspace)
	names := []string{"read_file", "list_dir", "write_file", "edit_file", "search_text", "find_files"}
	items := make([]Tool, 0, len(names))
	enabled := make(map[string]bool, len(names))
	for _, name := range names {
		items = append(items, identity.Wrap(descriptionTestTool{name: name}))
		enabled[name] = true
	}
	registry := New(items...)
	identity.Configure(cfg)
	off, err := json.Marshal(registry.Schemas(enabled))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(off), "Try once. If it's outside your workspace") {
		t.Fatalf("disabled split changed description: %s", off)
	}
	cfg.Shell.ServiceAccount.Enabled = true
	registry.Configure(cfg)
	on, err := json.Marshal(registry.Schemas(enabled))
	if err != nil {
		t.Fatal(err)
	}
	want := "Try once. If it's outside your workspace the operator will be asked; don't retry other paths."
	if strings.Count(string(on), want) != len(names) || string(on) == string(off) {
		t.Fatalf("enabled split schema=%s", on)
	}
	cfg.Shell.ServiceAccount.Enabled = false
	registry.Configure(cfg)
	again, err := json.Marshal(registry.Schemas(enabled))
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(off) {
		t.Fatalf("disabled schema did not return byte-identically: before=%s after=%s", off, again)
	}
}

func TestFileIdentityFailureOffersOperatorOverride(t *testing.T) {
	identity := enabledFileIdentity(t, &fileIdentityTestCredential{password: []byte{1, 2, 3}})
	identity.run = func(config.ShellServiceAccount, []byte, func() (string, error)) (string, error) {
		return "", &serviceFileIdentityError{err: errors.New("authentication failed")}
	}
	detail := identity.Wrap(NewReadFile(config.Defaults(t.TempDir()).Tools.ReadFile)).(DetailedTool).CallDetailed(
		context.Background(), &session.Session{Workspace: t.TempDir()}, map[string]any{"path": "missing.txt"},
	)
	if detail.Err != nil || detail.OperatorOverrideReason != "authentication failed" {
		t.Fatalf("detail=%+v", detail)
	}
}
