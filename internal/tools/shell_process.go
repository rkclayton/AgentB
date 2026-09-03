package tools

import (
	"fmt"
	"os/exec"

	"harness/internal/config"
)

type runningShellProcess interface {
	Wait() (int, error)
	KillTree()
}

type execShellProcess struct{ cmd *exec.Cmd }

func startHarnessProcess(executable string, argv []string, workspace string, output *lockedBuffer) (runningShellProcess, error) {
	cmd := exec.Command(executable, argv...)
	cmd.Dir = workspace
	setupProcess(cmd)
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execShellProcess{cmd: cmd}, nil
}

func (p *execShellProcess) Wait() (int, error) {
	err := p.cmd.Wait()
	if err == nil {
		return 0, nil
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), nil
	}
	return 0, err
}

func (p *execShellProcess) KillTree() { killProcessTree(p.cmd.Process.Pid) }

type serviceProcessStarter func(string, []string, string, []string, config.ShellServiceAccount, []byte, *lockedBuffer) (runningShellProcess, error)

type serviceSpawnError struct {
	kind string
	err  error
}

func (e *serviceSpawnError) Error() string {
	if e.err == nil {
		return e.kind
	}
	return fmt.Sprintf("%s: %v", e.kind, e.err)
}

func serviceSpawnReason(err error) string {
	if typed, ok := err.(*serviceSpawnError); ok {
		return typed.Error()
	}
	return fmt.Sprintf("service-account process API error: %v", err)
}
