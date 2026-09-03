package web

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) hostHardening(w http.ResponseWriter, r *http.Request) {
	if s.hardening == nil {
		writeError(w, http.StatusConflict, "host-hardening runtime is unavailable", "shell.service_account")
		return
	}
	serverID := r.URL.Query().Get("server_id")
	if r.Method == http.MethodGet {
		request, err := s.hardeningRequest(serverID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error(), "servers")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		status, err := s.hardening.Status(ctx, request)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "shell.service_account")
			return
		}
		writeJSON(w, http.StatusOK, status)
		return
	}
	if r.Method != http.MethodPost {
		method(w)
		return
	}
	if !s.accountMu.TryLock() {
		writeError(w, http.StatusConflict, "another service-account or hardening operation is already in progress", "shell.service_account")
		return
	}
	defer s.accountMu.Unlock()
	var body struct {
		Action   string `json:"action"`
		ServerID string `json:"server_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Action != "apply" && body.Action != "verify" && body.Action != "remove" {
		writeError(w, http.StatusBadRequest, "action must be apply, verify, or remove", "action")
		return
	}
	request, err := s.hardeningRequest(body.ServerID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "servers")
		return
	}
	if body.Action == "apply" {
		if s.registry != nil {
			for _, item := range s.registry.List() {
				if item.IsRunning() {
					writeError(w, http.StatusConflict, "stop all AgentB runs before applying host protections", "sessions")
					return
				}
			}
		}
		cfg := s.ConfigSnapshot()
		if !cfg.Shell.ServiceAccount.Enabled || s.credential == nil || !s.credential.Status().Stored {
			writeError(w, http.StatusConflict, "create, store, enable, and test the service identity before applying host protections", "shell.service_account")
			return
		}
		if s.account == nil {
			writeError(w, http.StatusConflict, "service-account inspection is unavailable", "shell.service_account")
			return
		}
		inspectContext, inspectCancel := context.WithTimeout(context.Background(), 10*time.Second)
		account, inspectErr := s.account.Status(inspectContext, request.AccountName)
		inspectCancel()
		if inspectErr != nil || !account.Exists || !account.Enabled || account.Administrator {
			writeError(w, http.StatusConflict, "the configured service account must exist, be enabled, and remain non-administrator", "shell.service_account")
			return
		}
		if s.shellTest == nil {
			writeError(w, http.StatusConflict, "service-identity spawn test is unavailable", "shell.service_account")
			return
		}
		testContext, testCancel := context.WithTimeout(context.Background(), 30*time.Second)
		message, testErr := s.shellTest(testContext)
		testCancel()
		if testErr != nil {
			writeError(w, http.StatusConflict, "service-identity spawn test failed: "+message, "shell.service_account")
			return
		}
	}

	operationContext, operationCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	result, runErr := s.hardening.Run(operationContext, body.Action, request)
	operationCancel()
	if runErr != nil {
		message := runErr.Error()
		if result.Attempted {
			message += "; the hardening operation started and may be partial, so run Verify before continuing"
		}
		writeError(w, http.StatusInternalServerError, message, "shell.service_account")
		return
	}
	statusContext, statusCancel := context.WithTimeout(context.Background(), 30*time.Second)
	status, statusErr := s.hardening.Status(statusContext, request)
	statusCancel()
	if statusErr != nil {
		writeError(w, http.StatusInternalServerError, "operation completed but status inspection failed: "+statusErr.Error(), "shell.service_account")
		return
	}
	message := map[string]string{
		"apply":  "host protections applied and verified",
		"verify": "host-protection verification complete",
		"remove": "host protections removed",
	}[body.Action]
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": message, "status": status})
}
