package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"harness/internal/hardening"
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
		s.writeHardeningStatus(w, status)
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
	startMessage := map[string]string{
		"apply":  "Checking host-protection prerequisites…",
		"verify": "Verifying ACL and outbound policy…",
		"remove": "Checking host-protection removal prerequisites…",
	}[body.Action]
	s.beginHardening(body.Action, startMessage)
	fail := func(status int, message, field string) {
		s.finishHardening("failed", message)
		writeError(w, status, message, field)
	}
	request, err := s.hardeningRequest(body.ServerID)
	if err != nil {
		fail(http.StatusBadRequest, err.Error(), "servers")
		return
	}
	if body.Action == "apply" {
		if s.registry != nil {
			for _, item := range s.registry.List() {
				if item.IsRunning() {
					fail(http.StatusConflict, "stop all Agent_b runs before applying host protections", "sessions")
					return
				}
			}
		}
		cfg := s.ConfigSnapshot()
		if !cfg.Shell.ServiceAccount.Enabled || s.credential == nil || !s.credential.Status().Stored {
			fail(http.StatusConflict, "create, store, and enable the service identity before applying host protections", "shell.service_account")
			return
		}
		if s.account == nil {
			fail(http.StatusConflict, "service-account inspection is unavailable", "shell.service_account")
			return
		}
		inspectContext, inspectCancel := context.WithTimeout(context.Background(), 10*time.Second)
		account, inspectErr := s.account.Status(inspectContext, request.AccountName)
		inspectCancel()
		if inspectErr != nil || !account.Exists || !account.Enabled || account.Administrator {
			fail(http.StatusConflict, "the configured service account must exist, be enabled, and remain non-administrator", "shell.service_account")
			return
		}
		if s.shellTest == nil {
			fail(http.StatusConflict, "service-identity spawn test is unavailable", "shell.service_account")
			return
		}
	}
	if body.Action == "verify" {
		s.progressHardening("Verifying ACL and outbound policy…")
	} else {
		s.progressHardening("Waiting for Windows approval. Check the taskbar if the UAC prompt is not visible…")
	}

	operationContext, operationCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	result, runErr := s.hardening.Run(operationContext, body.Action, request)
	operationCancel()
	if runErr != nil {
		message := runErr.Error()
		if result.Attempted {
			message += "; the hardening operation started and may be partial, so run Verify before continuing"
		}
		fail(http.StatusInternalServerError, message, "shell.service_account")
		return
	}
	s.progressHardening("Windows operation finished; checking the resulting policy…")
	statusContext, statusCancel := context.WithTimeout(context.Background(), 30*time.Second)
	status, statusErr := s.hardening.Status(statusContext, request)
	statusCancel()
	if statusErr != nil {
		fail(http.StatusInternalServerError, "operation completed but status inspection failed: "+statusErr.Error(), "shell.service_account")
		return
	}
	message := map[string]string{
		"apply":  "host protections applied and verified",
		"verify": "host-protection verification complete",
		"remove": "host protections removed",
	}[body.Action]
	if (body.Action == "apply" || body.Action == "verify") && !status.Applied {
		message = hardeningDriftMessage(status)
		s.finishHardening("failed", message)
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": message, "status": status, "operation": s.hardeningOperation()})
		return
	}
	if body.Action == "apply" {
		s.progressHardening("Host protections verified; testing service-account workspace access…")
		testContext, testCancel := context.WithTimeout(context.Background(), 30*time.Second)
		testMessage, testErr := s.shellTest(testContext)
		testCancel()
		if testErr != nil {
			message = "host protections were applied, but service-account workspace access failed: " + testMessage
			s.finishHardening("failed", message)
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": message, "status": status, "operation": s.hardeningOperation()})
			return
		}
	}
	s.finishHardening("succeeded", message)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": message, "status": status, "operation": s.hardeningOperation()})
}

func hardeningDriftMessage(status hardening.Status) string {
	parts := make([]string, 0, 2)
	if !status.ACL.Applied {
		parts = append(parts, status.ACL.Summary)
	}
	if !status.Firewall.Applied {
		parts = append(parts, status.Firewall.Summary)
	}
	if len(parts) == 0 {
		return "host-protection verification failed"
	}
	return "host-protection verification failed: " + strings.Join(parts, "; ")
}
