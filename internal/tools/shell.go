package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"harness/internal/config"
	"harness/internal/session"
)

// Shell executes commands without an OS sandbox. Its jail, timeout, approval gate,
// and deny list reduce accidents but are not a security boundary.
type Shell struct {
	mu  sync.RWMutex
	cfg config.Shell
}

func NewShell(cfg config.Shell) *Shell { return &Shell{cfg: cfg} }
func (*Shell) Name() string            { return "shell" }
func (*Shell) Description() string {
	return "Run a non-interactive shell command from the workspace root with a timeout. Returns the exit code and combined output, cut to head and tail if long. For file listing or reading use list_dir, glob, or read_file."
}
func (s *Shell) Schema() map[string]any {
	cfg := s.config()
	return map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}, "timeout_s": map[string]any{"type": "integer", "default": cfg.TimeoutS, "maximum": cfg.MaxTimeoutS}}, "required": []string{"command"}}
}
func (s *Shell) Call(ctx context.Context, item *session.Session, args map[string]any) (string, error) {
	cfg := s.config()
	command, ok := args["command"].(string)
	if !ok || strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("command is required")
	}
	for _, denied := range cfg.Deny {
		if denied != "" && strings.Contains(strings.ToLower(command), strings.ToLower(denied)) {
			return "", fmt.Errorf("command blocked by deny list")
		}
	}
	timeout := number(args["timeout_s"], cfg.TimeoutS)
	if timeout <= 0 {
		timeout = cfg.TimeoutS
	}
	if timeout > cfg.MaxTimeoutS {
		timeout = cfg.MaxTimeoutS
	}
	argv := append(append([]string(nil), cfg.Command[1:]...), command)
	cmd := exec.Command(cfg.Command[0], argv...)
	cmd.Dir = item.Workspace
	setupProcess(cmd)
	var output lockedBuffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		killProcessTree(cmd.Process.Pid)
		<-done
		return "", ctx.Err()
	case <-timer.C:
		killProcessTree(cmd.Process.Pid)
		<-done
		partial := cutOutput(output.String(), cfg.MaxOutputLinesHead, cfg.MaxOutputLinesTail)
		if partial != "" {
			return "", fmt.Errorf("timed out after %ds; partial output:\n%s", timeout, partial)
		}
		return "", fmt.Errorf("timed out after %ds; partial output:", timeout)
	case err := <-done:
		code := 0
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				return "", err
			}
		}
		body := cutOutput(output.String(), cfg.MaxOutputLinesHead, cfg.MaxOutputLinesTail)
		if body == "" {
			return fmt.Sprintf("exit=%d", code), nil
		}
		return fmt.Sprintf("exit=%d\n%s", code, body), nil
	}
}
func (s *Shell) Configure(value config.Config) { s.mu.Lock(); s.cfg = value.Shell; s.mu.Unlock() }
func (s *Shell) config() config.Shell          { s.mu.RLock(); defer s.mu.RUnlock(); return s.cfg }

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *lockedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }
func cutOutput(value string, head, tail int) string {
	value = strings.TrimRight(normalizeLF(value), "\n")
	if value == "" {
		return ""
	}
	lines := strings.Split(value, "\n")
	if len(lines) <= head+tail {
		return value
	}
	omitted := len(lines) - head - tail
	kept := append([]string(nil), lines[:head]...)
	kept = append(kept, fmt.Sprintf("[… %d lines omitted …]", omitted))
	kept = append(kept, lines[len(lines)-tail:]...)
	return strings.Join(kept, "\n")
}
