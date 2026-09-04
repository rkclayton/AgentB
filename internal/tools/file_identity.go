package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"harness/internal/config"
	"harness/internal/session"
)

// FileIdentity applies the same configured Windows identity to the built-in
// file tools that Shell uses. When enabled, absolute paths are authorized by
// that identity's OS token instead of by the harness operator's token.
type FileIdentity struct {
	mu         sync.RWMutex
	service    config.ShellServiceAccount
	credential shellCredentialReader
	run        serviceFileRunner
}

type serviceFileRunner func(config.ShellServiceAccount, []byte, func() (string, error)) (string, error)

type serviceFileIdentityError struct{ err error }

func (e *serviceFileIdentityError) Error() string { return e.err.Error() }
func (e *serviceFileIdentityError) Unwrap() error { return e.err }

func NewFileIdentity(credential shellCredentialReader) *FileIdentity {
	return &FileIdentity{credential: credential, run: runAsServiceFileIdentity}
}

func (p *FileIdentity) Configure(cfg config.Config) {
	p.mu.Lock()
	p.service = cfg.Shell.ServiceAccount
	p.mu.Unlock()
}

func (p *FileIdentity) Wrap(tool Tool) Tool {
	return &identityFileTool{tool: tool, identity: p}
}

func (p *FileIdentity) snapshot() (config.ShellServiceAccount, shellCredentialReader, serviceFileRunner) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.service, p.credential, p.run
}

type identityFileTool struct {
	tool     Tool
	identity *FileIdentity
}

func (t *identityFileTool) Name() string           { return t.tool.Name() }
func (t *identityFileTool) Description() string    { return t.tool.Description() }
func (t *identityFileTool) Schema() map[string]any { return t.tool.Schema() }

func (t *identityFileTool) Configure(cfg config.Config) {
	if configurableTool, ok := t.tool.(configurable); ok {
		configurableTool.Configure(cfg)
	}
	t.identity.Configure(cfg)
}

func (t *identityFileTool) Call(ctx context.Context, s *session.Session, args map[string]any) (string, error) {
	detail := t.CallDetailed(ctx, s, args)
	return detail.Content, detail.Err
}

func (t *identityFileTool) CallDetailed(ctx context.Context, s *session.Session, args map[string]any) CallDetail {
	service, credential, runner := t.identity.snapshot()
	if !service.Enabled {
		result, err := t.tool.Call(ctx, s, args)
		return CallDetail{Content: result, Err: err}
	}
	if credential == nil {
		return fileIdentityOverride("service-account credential is not configured")
	}
	password, err := credential.Read()
	if err != nil {
		return fileIdentityOverride(err.Error())
	}
	defer clearBytes(password)
	result, err := runner(service, password, func() (string, error) {
		return t.tool.Call(withOSPathPolicy(ctx), s, args)
	})
	if err == nil {
		return CallDetail{Content: result}
	}
	var identityErr *serviceFileIdentityError
	if errors.As(err, &identityErr) {
		return fileIdentityOverride(identityErr.Error())
	}
	if errors.Is(err, os.ErrPermission) {
		return fileIdentityOverride("service account was denied permission for the requested path")
	}
	return CallDetail{Err: err}
}

// CallAsOperator is dispatcher-only. It deliberately bypasses both the
// service identity and workspace path boundary for one explicitly approved
// replay of the exact tool call.
func (t *identityFileTool) CallAsOperator(ctx context.Context, s *session.Session, args map[string]any) (string, error) {
	return t.tool.Call(withOSPathPolicy(ctx), s, args)
}

func fileIdentityOverride(reason string) CallDetail {
	return CallDetail{
		Content:                fmt.Sprintf("service-account file operation was not completed: %s", reason),
		OperatorOverrideReason: reason,
	}
}
