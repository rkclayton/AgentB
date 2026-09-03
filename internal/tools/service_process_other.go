//go:build !windows

package tools

import (
	"harness/internal/config"
)

func startServiceAccountProcess(string, []string, string, []string, config.ShellServiceAccount, []byte, *lockedBuffer) (runningShellProcess, error) {
	return nil, &serviceSpawnError{kind: "service-account spawning is supported only on Windows"}
}

func minimalShellEnvironment(config.ShellServiceAccount, string) []string { return nil }
