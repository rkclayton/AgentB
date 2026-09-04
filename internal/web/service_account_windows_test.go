//go:build windows

package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/credential"
	"harness/internal/events"
	"harness/internal/serviceaccount"
	"harness/internal/tools"
)

type fakeAccountManager struct {
	status       serviceaccount.Status
	setupResult  serviceaccount.SetupResult
	setupErr     error
	setupCalls   int
	setupAccount string
	setupPath    string
	setupReset   bool
}

func (m *fakeAccountManager) Status(context.Context, string) (serviceaccount.Status, error) {
	status := m.status
	if m.setupCalls > 0 && m.setupErr == nil {
		status.Exists, status.Enabled = true, true
	}
	return status, nil
}

func (m *fakeAccountManager) Setup(_ context.Context, account, path string, reset bool) (serviceaccount.SetupResult, error) {
	m.setupCalls++
	m.setupAccount, m.setupPath, m.setupReset = account, path, reset
	return m.setupResult, m.setupErr
}

func serviceAccountTestServer(t *testing.T, manager serviceaccount.Manager) (*Server, *credential.Store, string) {
	t.Helper()
	root := t.TempDir()
	configPath := root + `\harness.json`
	cfg := config.Defaults(root)
	if err := cfg.Save(configPath); err != nil {
		t.Fatal(err)
	}
	store := credential.New(configPath)
	shell := tools.NewShell(cfg.Shell)
	shell.Configure(cfg)
	shell.SetCredentialStore(store)
	server := New(&cfg, configPath, root, events.NewBus())
	server.SetShellSecurity(store, shell)
	server.SetServiceAccountManager(manager)
	server.shellTest = func(context.Context) (string, error) {
		return "service-account shell spawn succeeded", nil
	}
	return server, store, configPath
}

func randomTestPassword(t *testing.T) string {
	t.Helper()
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(value)
}

func TestServiceAccountSetupStoresEnablesWithoutPrematureWorkspaceTest(t *testing.T) {
	manager := &fakeAccountManager{
		status:      serviceaccount.Status{Supported: true, Account: "agentb-svc"},
		setupResult: serviceaccount.SetupResult{Attempted: true},
	}
	server, store, configPath := serviceAccountTestServer(t, manager)
	testCalls := 0
	server.shellTest = func(context.Context) (string, error) {
		testCalls++
		return "workspace is not granted yet", errors.New("premature workspace test")
	}
	password := randomTestPassword(t)
	body := `{"action":"create","password":"` + password + `","confirmation":"` + password + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/service-account", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if bytes.Contains(response.Body.Bytes(), []byte(password)) {
		t.Fatalf("response returned password: %s", response.Body)
	}
	if !strings.Contains(response.Body.String(), `"ok":true`) || !strings.Contains(response.Body.String(), "apply host protection") || testCalls != 0 || manager.setupCalls != 1 || manager.setupAccount != "agentb-svc" || manager.setupPath != store.Path() || manager.setupReset {
		t.Fatalf("unexpected setup result: calls=%d account=%q path=%q reset=%v body=%s", manager.setupCalls, manager.setupAccount, manager.setupPath, manager.setupReset, response.Body)
	}
	stored, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != password {
		t.Fatal("stored credential does not match submitted credential")
	}
	clearSecret(stored)
	loaded, _, _, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Shell.ServiceAccount.Enabled || loaded.Shell.ServiceAccount.Account != "agentb-svc" {
		t.Fatalf("service identity was not enabled: %+v", loaded.Shell.ServiceAccount)
	}
}

func TestServiceAccountSetupRejectsMismatchBeforeMutation(t *testing.T) {
	manager := &fakeAccountManager{status: serviceaccount.Status{Supported: true, Account: "agentb-svc"}}
	server, store, _ := serviceAccountTestServer(t, manager)
	password := randomTestPassword(t)
	request := httptest.NewRequest(http.MethodPost, "/api/service-account", strings.NewReader(`{"action":"create","password":"`+password+`","confirmation":"different"}`))
	request.Header.Set("Content-Type", "application/json")
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || manager.setupCalls != 0 || store.Status().Stored {
		t.Fatalf("mismatch mutated state: status=%d calls=%d stored=%v body=%s", response.Code, manager.setupCalls, store.Status().Stored, response.Body)
	}
	if bytes.Contains(response.Body.Bytes(), []byte(password)) {
		t.Fatal("mismatch response returned password")
	}
}

func TestServiceAccountCanceledElevationRestoresCredential(t *testing.T) {
	manager := &fakeAccountManager{
		status:      serviceaccount.Status{Supported: true, Account: "agentb-svc"},
		setupResult: serviceaccount.SetupResult{},
		setupErr:    errors.New("Windows elevation was canceled"),
	}
	server, store, _ := serviceAccountTestServer(t, manager)
	previous := randomTestPassword(t)
	if err := store.Write([]byte(previous)); err != nil {
		t.Fatal(err)
	}
	password := randomTestPassword(t)
	request := httptest.NewRequest(http.MethodPost, "/api/service-account", strings.NewReader(`{"action":"create","password":"`+password+`","confirmation":"`+password+`"}`))
	request.Header.Set("Content-Type", "application/json")
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	stored, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer clearSecret(stored)
	if string(stored) != previous {
		t.Fatal("prior credential was not restored after canceled elevation")
	}
	if bytes.Contains(response.Body.Bytes(), []byte(password)) || bytes.Contains(response.Body.Bytes(), []byte(previous)) {
		t.Fatal("cancellation response returned a credential")
	}
}

func TestServiceAccountAttemptedFailureRetainsSubmittedCredentialAndWarns(t *testing.T) {
	manager := &fakeAccountManager{
		status:      serviceaccount.Status{Supported: true, Account: "agentb-svc"},
		setupResult: serviceaccount.SetupResult{Attempted: true},
		setupErr:    errors.New("setup validation failed"),
	}
	server, store, _ := serviceAccountTestServer(t, manager)
	password := randomTestPassword(t)
	request := httptest.NewRequest(http.MethodPost, "/api/service-account", strings.NewReader(`{"action":"create","password":"`+password+`","confirmation":"`+password+`"}`))
	request.Header.Set("Content-Type", "application/json")
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"attempted":true`) || !strings.Contains(response.Body.String(), "potentially partial") && !strings.Contains(response.Body.String(), "script started") {
		t.Fatalf("partial failure was not loud: status=%d body=%s", response.Code, response.Body)
	}
	stored, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer clearSecret(stored)
	if string(stored) != password {
		t.Fatal("submitted credential was not retained after a potentially partial password change")
	}
	if bytes.Contains(response.Body.Bytes(), []byte(password)) {
		t.Fatal("partial failure response returned the password")
	}
}

func TestServiceAccountPasswordValidation(t *testing.T) {
	valid := randomTestPassword(t)
	tests := []struct {
		name         string
		password     string
		confirmation string
		message      string
	}{
		{name: "empty", message: "required"},
		{name: "short", password: "short", confirmation: "short", message: "14"},
		{name: "line break", password: valid + "\n", confirmation: valid + "\n", message: "line break"},
		{name: "command", password: "Get-" + valid, confirmation: "Get-" + valid, message: "pasted command"},
		{name: "mismatch", password: valid, confirmation: valid + "x", message: "do not match"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAccountPassword(test.password, test.confirmation)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error=%v, want message containing %q", err, test.message)
			}
		})
	}
	if err := validateAccountPassword(valid, valid); err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
}
