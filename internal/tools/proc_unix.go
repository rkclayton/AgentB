//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

func setupProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func killProcessTree(pid int)    { _ = syscall.Kill(-pid, syscall.SIGKILL) }
