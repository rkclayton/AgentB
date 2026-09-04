package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"harness/internal/events"
)

var localAccountName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func (s *Server) serviceAccount(w http.ResponseWriter, r *http.Request) {
	if s.account == nil || s.credential == nil || s.shell == nil {
		writeError(w, http.StatusConflict, "service-account setup runtime is unavailable", "shell.service_account")
		return
	}
	account, domain := s.configuredServiceAccount()
	if !localAccountName.MatchString(account) {
		writeError(w, http.StatusBadRequest, "account must contain only letters, numbers, dot, underscore, or hyphen", "shell.service_account.account")
		return
	}
	if domain != "." && !strings.EqualFold(domain, os.Getenv("COMPUTERNAME")) {
		writeError(w, http.StatusBadRequest, "web setup creates local accounts only; set domain to . or this computer", "shell.service_account.domain")
		return
	}

	switch r.Method {
	case http.MethodGet:
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		status, err := s.account.Status(ctx, account)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "shell.service_account")
			return
		}
		writeJSON(w, http.StatusOK, status)
	case http.MethodPost:
		s.setupServiceAccount(w, r, account)
	default:
		method(w)
	}
}

func (s *Server) setupServiceAccount(w http.ResponseWriter, r *http.Request, account string) {
	if !s.accountMu.TryLock() {
		writeError(w, http.StatusConflict, "another service-account setup is already in progress", "shell.service_account")
		return
	}
	defer s.accountMu.Unlock()
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var body struct {
		Action       string `json:"action"`
		Password     string `json:"password"`
		Confirmation string `json:"confirmation"`
	}
	if !decode(w, r, &body) {
		return
	}
	reset := body.Action == "reset"
	if body.Action != "create" && !reset {
		body.Password, body.Confirmation = "", ""
		writeError(w, http.StatusBadRequest, "action must be create or reset", "action")
		return
	}
	if err := validateAccountPassword(body.Password, body.Confirmation); err != nil {
		body.Password, body.Confirmation = "", ""
		writeError(w, http.StatusBadRequest, err.Error(), "shell.service_account.setup_password")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	status, err := s.account.Status(ctx, account)
	cancel()
	if err != nil {
		body.Password, body.Confirmation = "", ""
		writeError(w, http.StatusInternalServerError, err.Error(), "shell.service_account")
		return
	}
	if !status.Supported {
		body.Password, body.Confirmation = "", ""
		writeError(w, http.StatusBadRequest, "local service-account setup is supported only on Windows", "shell.service_account")
		return
	}
	if status.Exists && !reset {
		body.Password, body.Confirmation = "", ""
		writeError(w, http.StatusConflict, "the local account already exists; use Reset password", "shell.service_account")
		return
	}
	if !status.Exists && reset {
		body.Password, body.Confirmation = "", ""
		writeError(w, http.StatusConflict, "the local account does not exist; use Create account", "shell.service_account")
		return
	}
	if status.Administrator {
		body.Password, body.Confirmation = "", ""
		writeError(w, http.StatusConflict, "refusing to manage an account that belongs to Administrators", "shell.service_account")
		return
	}

	previous, hadPrevious, err := s.previousCredential()
	if err != nil {
		body.Password, body.Confirmation = "", ""
		writeError(w, http.StatusInternalServerError, "the existing credential could not be preserved before setup", "shell.service_account.password")
		return
	}
	defer clearSecret(previous)
	password := []byte(body.Password)
	body.Password, body.Confirmation = "", ""
	if err := s.credential.Write(password); err != nil {
		clearSecret(password)
		writeError(w, http.StatusInternalServerError, err.Error(), "shell.service_account.password")
		return
	}
	clearSecret(password)

	// The request is deliberately detached while Windows displays UAC. Closing
	// the browser must not strand an account operation halfway through.
	setupContext, setupCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	result, setupErr := s.account.Setup(setupContext, account, s.credential.Path(), reset)
	setupCancel()
	if setupErr != nil {
		if !result.Attempted {
			if restoreErr := s.restoreCredential(previous, hadPrevious); restoreErr != nil {
				writeError(w, http.StatusInternalServerError, setupErr.Error()+"; restoring the prior credential also failed", "shell.service_account")
				return
			}
			writeError(w, http.StatusBadRequest, setupErr.Error()+"; no account change was attempted", "shell.service_account")
			return
		}
		credentialStatus := s.credential.Status()
		s.bus.Publish(events.New(events.ShellCredential, "", "", credentialStatus))
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":      setupErr.Error() + "; the elevated script started, so inspect the account and use Reset password before continuing",
			"field":      "shell.service_account",
			"credential": credentialStatus,
			"attempted":  true,
		})
		return
	}

	masked, err := s.enableConfiguredServiceAccount(account)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "the account and credential were updated, but enabling the identity split failed: " + err.Error(),
			"field": "shell.service_account.enabled",
		})
		return
	}
	credentialStatus := s.credential.Status()
	s.bus.Publish(events.New(events.ShellCredential, "", "", credentialStatus))
	s.bus.Publish(events.New(events.ConfigChanged, "", "", map[string]any{"config": masked}))

	currentStatus := status
	inspectContext, inspectCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if inspected, inspectErr := s.account.Status(inspectContext, account); inspectErr == nil {
		currentStatus = inspected
	}
	inspectCancel()
	response := map[string]any{
		"ok":         true,
		"message":    "account and credential updated and authenticated; apply host protection to grant workspace access, then test identity",
		"account":    currentStatus,
		"credential": credentialStatus,
		"identity":   s.shell.IdentityStatus(),
		"config":     masked,
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) configuredServiceAccount() (string, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.cfg.Shell.ServiceAccount.Account), strings.TrimSpace(s.cfg.Shell.ServiceAccount.Domain)
}

func (s *Server) previousCredential() ([]byte, bool, error) {
	if !s.credential.Status().Stored {
		return nil, false, nil
	}
	value, err := s.credential.Read()
	return value, err == nil, err
}

func (s *Server) restoreCredential(previous []byte, stored bool) error {
	if !stored {
		return s.credential.Clear()
	}
	return s.credential.Write(previous)
}

func (s *Server) enableConfiguredServiceAccount(account string) (any, error) {
	s.mu.Lock()
	previous := *s.cfg
	s.cfg.Shell.ServiceAccount.Account = account
	s.cfg.Shell.ServiceAccount.Domain = "."
	s.cfg.Shell.ServiceAccount.Enabled = true
	if err := s.cfg.Save(s.configPath); err != nil {
		*s.cfg = previous
		s.mu.Unlock()
		return nil, err
	}
	next := *s.cfg
	masked := s.cfg.Masked()
	s.mu.Unlock()
	s.shell.Configure(next)
	return masked, nil
}

func validateAccountPassword(password, confirmation string) error {
	if password == "" {
		return errors.New("password is required")
	}
	if strings.ContainsAny(password, "\r\n") || strings.ContainsAny(confirmation, "\r\n") {
		return errors.New("password cannot contain a line break")
	}
	if utf8.RuneCountInString(password) < 14 {
		return errors.New("password must contain at least 14 characters")
	}
	value := strings.ToLower(strings.TrimLeft(password, " \t"))
	for _, prefix := range []string{"get-", "set-", "new-", `.\`, "cd ", "git "} {
		if strings.HasPrefix(value, prefix) {
			return errors.New("the value looks like a pasted command, not a password")
		}
	}
	if password != confirmation {
		return errors.New("the two password entries do not match")
	}
	return nil
}

func clearSecret(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
