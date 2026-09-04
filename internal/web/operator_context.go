package web

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"harness/internal/events"
	"harness/internal/tools"
)

func (s *Server) setOperatorContext(enabled bool, reason string, expectedEpoch uint64) tools.ShellIdentityStatus {
	s.mu.RLock()
	timeoutMinutes := s.cfg.Shell.OperatorContextTimeoutMinutes
	s.mu.RUnlock()

	s.operatorMu.Lock()
	if expectedEpoch != 0 && expectedEpoch != s.operatorEpoch {
		status := s.currentOperatorStatusLocked()
		s.operatorMu.Unlock()
		return status
	}
	s.operatorEpoch++
	epoch := s.operatorEpoch
	if s.operatorTimer != nil {
		s.operatorTimer.Stop()
		s.operatorTimer = nil
	}
	s.operatorEnabled = enabled
	s.operatorExpires = ""
	if enabled {
		duration := s.operatorDuration(timeoutMinutes)
		expires := time.Now().Add(duration).UTC()
		s.operatorExpires = expires.Format(time.RFC3339)
		s.operatorTimer = time.AfterFunc(duration, func() {
			s.setOperatorContext(false, "automatic timeout expired", epoch)
		})
	}
	s.operatorMu.Unlock()

	runtimeConfig := s.ConfigSnapshot()
	if s.runner != nil {
		s.runner.Configure(runtimeConfig)
	} else if s.shell != nil {
		s.shell.Configure(runtimeConfig)
	}
	status := tools.ShellIdentityStatus{}
	if s.shell != nil {
		status = s.shell.IdentityStatus()
	}
	s.publishOperatorContext(enabled, reason, status.OperatorContextExpiresAt)
	if s.bus != nil {
		s.bus.Publish(events.New(events.ConfigChanged, "", "", map[string]any{"config": runtimeConfig.Masked()}))
	}
	return status
}

func (s *Server) currentOperatorStatusLocked() tools.ShellIdentityStatus {
	if !s.operatorEnabled {
		return tools.ShellIdentityStatus{}
	}
	return tools.ShellIdentityStatus{
		OperatorContext:          true,
		OperatorContextExpiresAt: s.operatorExpires,
		Reason:                   "operator context is enabled; tools are running as the Windows account that launched Agent_b",
	}
}

func (s *Server) publishOperatorContext(enabled bool, reason, expiresAt string) {
	data := map[string]any{"enabled": enabled, "reason": reason, "expires_at": expiresAt}
	log.Printf("SECURITY: operator context enabled=%t reason=%q expires_at=%q", enabled, reason, expiresAt)
	if s.bus == nil {
		return
	}
	s.bus.Publish(events.New(events.OperatorContext, "", "", data))
	if s.registry == nil {
		return
	}
	for _, item := range s.registry.List() {
		s.bus.Publish(events.New(events.OperatorContext, item.ID, "", data))
	}
}

func operatorContextPatch(patch map[string]any) (enabled bool, present bool, only bool, err error) {
	raw, ok := patch["shell"]
	if !ok {
		return false, false, false, nil
	}
	shell, ok := raw.(map[string]any)
	if !ok {
		return false, false, false, nil
	}
	value, present := shell["operator_context"]
	if !present {
		if _, protected := shell["operator_context_timeout_minutes"]; protected {
			return false, false, false, fmt.Errorf("shell.operator_context_timeout_minutes can be changed only in the protected configuration file while Agent_b is stopped")
		}
		return false, false, false, nil
	}
	enabled, ok = value.(bool)
	if !ok {
		return false, true, false, fmt.Errorf("shell.operator_context must be boolean")
	}
	return enabled, true, len(patch) == 1 && len(shell) == 1, nil
}

func (s *Server) applyOperatorContextPatch(w http.ResponseWriter, r *http.Request, patch map[string]any) bool {
	enabled, present, only, err := operatorContextPatch(patch)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error(), "shell.operator_context")
		return true
	}
	if !present {
		return false
	}
	if !only {
		writeError(w, http.StatusBadRequest, "save other settings separately from the operator-context switch", "shell.operator_context")
		return true
	}
	reason := "disabled by operator request"
	if enabled {
		reason = "enabled by verified operator process"
	}
	s.setOperatorContext(enabled, reason, 0)
	writeJSON(w, http.StatusOK, s.ConfigSnapshot().Masked())
	return true
}

func protectedShellConfigField(patch map[string]any) string {
	raw, ok := patch["shell"]
	if !ok {
		return ""
	}
	shell, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	for _, field := range []string{"operator_context", "operator_context_timeout_minutes", "service_account"} {
		if _, present := shell[field]; present {
			return "shell." + field
		}
	}
	return ""
}

func (s *Server) requireOperatorConfigRequest(w http.ResponseWriter, r *http.Request, patch map[string]any) bool {
	field := protectedShellConfigField(patch)
	if field == "" {
		return true
	}
	if err := s.operatorRequest(r); err != nil {
		log.Printf("SECURITY: protected config mutation refused field=%s: %v", field, err)
		writeError(w, http.StatusForbidden, "security-sensitive shell settings can be changed only by a local process owned by the Windows account that launched Agent_b", field)
		return false
	}
	return true
}
