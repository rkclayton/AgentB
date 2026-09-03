//go:build !windows

package hardening

import "context"

type unsupportedManager struct{}

func New(string, string, string) Manager { return unsupportedManager{} }
func (unsupportedManager) Status(context.Context, Request) (Status, error) {
	return Status{Supported: false}, nil
}
func (unsupportedManager) Run(context.Context, string, Request) (RunResult, error) {
	return RunResult{}, ErrUnsupported
}
