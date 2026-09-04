//go:build windows

package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"harness/internal/hardening"
	"harness/internal/serviceaccount"
	"harness/internal/session"
)

type fakeHardeningManager struct {
	status     hardening.Status
	runAction  string
	runRequest hardening.Request
	runCalls   int
	runErr     error
}

func (m *fakeHardeningManager) Status(context.Context, hardening.Request) (hardening.Status, error) {
	return m.status, nil
}
func (m *fakeHardeningManager) Run(_ context.Context, action string, request hardening.Request) (hardening.RunResult, error) {
	m.runCalls++
	m.runAction, m.runRequest = action, request
	return hardening.RunResult{Attempted: true}, m.runErr
}

func TestHardeningEndpointAppliesBeforeTestingWorkspaceIdentity(t *testing.T) {
	account := &fakeAccountManager{status: serviceaccount.Status{Supported: true, Account: "agentb-svc", Exists: true, Enabled: true}}
	server, store, _ := serviceAccountTestServer(t, account)
	server.cfg.Servers[0].BaseURL = "http://198.51.100.10:8080/v1"
	server.cfg.Shell.ServiceAccount.Enabled = true
	server.registry = &session.Registry{}
	if err := store.Write([]byte(randomTestPassword(t))); err != nil {
		t.Fatal(err)
	}
	manager := &fakeHardeningManager{status: hardening.Status{Supported: true, Applied: true}}
	server.SetHardeningManager(manager)
	testCalls := 0
	server.shellTest = func(context.Context) (string, error) {
		testCalls++
		if manager.runCalls != 1 {
			return "workspace tested before protection was applied", errors.New("wrong operation order")
		}
		return "service-account shell spawn succeeded", nil
	}

	request := httptest.NewRequest(http.MethodPost, "/api/hardening", strings.NewReader(`{"action":"apply","server_id":"local"}`))
	request.Header.Set("Content-Type", "application/json")
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || manager.runCalls != 1 || manager.runAction != "apply" || testCalls != 1 {
		t.Fatalf("status=%d calls=%d action=%q tests=%d body=%s", response.Code, manager.runCalls, manager.runAction, testCalls, response.Body)
	}
	if manager.runRequest.ModelAddress != "198.51.100.10" || manager.runRequest.ModelPort != 8080 || manager.runRequest.AccountName != "agentb-svc" {
		t.Fatalf("unexpected hardening request: %+v", manager.runRequest)
	}
}

func TestHardeningReportsWorkspaceTestFailureAfterApplyingPolicy(t *testing.T) {
	account := &fakeAccountManager{status: serviceaccount.Status{Supported: true, Account: "agentb-svc", Exists: true, Enabled: true}}
	server, store, _ := serviceAccountTestServer(t, account)
	server.cfg.Servers[0].BaseURL = "http://127.0.0.1:8080"
	server.cfg.Shell.ServiceAccount.Enabled = true
	server.registry = &session.Registry{}
	if err := store.Write([]byte(randomTestPassword(t))); err != nil {
		t.Fatal(err)
	}
	manager := &fakeHardeningManager{status: hardening.Status{Supported: true, Applied: true}}
	server.SetHardeningManager(manager)
	server.shellTest = func(context.Context) (string, error) {
		return "service account cannot access the configured workspace", errors.New("directory unavailable")
	}

	request := httptest.NewRequest(http.MethodPost, "/api/hardening", strings.NewReader(`{"action":"apply","server_id":"local"}`))
	request.Header.Set("Content-Type", "application/json")
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || manager.runCalls != 1 || !strings.Contains(response.Body.String(), `"ok":false`) || !strings.Contains(response.Body.String(), "workspace access failed") {
		t.Fatalf("post-apply identity failure was not reported: status=%d calls=%d body=%s", response.Code, manager.runCalls, response.Body)
	}
}

func TestHardeningEndpointRejectsUnconfiguredIdentity(t *testing.T) {
	account := &fakeAccountManager{status: serviceaccount.Status{Supported: true, Account: "agentb-svc", Exists: true, Enabled: true}}
	server, _, _ := serviceAccountTestServer(t, account)
	server.cfg.Servers[0].BaseURL = "http://127.0.0.1:8080"
	server.registry = &session.Registry{}
	manager := &fakeHardeningManager{}
	server.SetHardeningManager(manager)

	request := httptest.NewRequest(http.MethodPost, "/api/hardening", strings.NewReader(`{"action":"apply","server_id":"local"}`))
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict || manager.runCalls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, manager.runCalls, response.Body)
	}
}

func TestHardeningStatusRejectsHostnameEndpoint(t *testing.T) {
	server, _, _ := serviceAccountTestServer(t, &fakeAccountManager{})
	server.cfg.Servers[0].BaseURL = "https://model.example.invalid/v1"
	server.SetHardeningManager(&fakeHardeningManager{})
	request := httptest.NewRequest(http.MethodGet, "/api/hardening?server_id=local", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "numeric") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestHardeningOperationSurvivesStatusRefresh(t *testing.T) {
	account := &fakeAccountManager{status: serviceaccount.Status{Supported: true, Account: "agentb-svc", Exists: true, Enabled: true}}
	server, store, _ := serviceAccountTestServer(t, account)
	server.cfg.Servers[0].BaseURL = "http://127.0.0.1:8080"
	server.cfg.Shell.ServiceAccount.Enabled = true
	server.registry = &session.Registry{}
	if err := store.Write([]byte(randomTestPassword(t))); err != nil {
		t.Fatal(err)
	}
	manager := &fakeHardeningManager{runErr: errors.New("test elevation failure")}
	server.SetHardeningManager(manager)

	request := httptest.NewRequest(http.MethodPost, "/api/hardening", strings.NewReader(`{"action":"apply","server_id":"local"}`))
	request.Header.Set("Content-Type", "application/json")
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/hardening?server_id=local", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	var body struct {
		Operation hardeningOperation `json:"operation"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Operation.State != "failed" || !strings.Contains(body.Operation.Message, "test elevation failure") || body.Operation.FinishedAt == "" {
		t.Fatalf("operation not retained: %+v", body.Operation)
	}
}

func TestHardeningVerifyReportsDriftAsFailure(t *testing.T) {
	server, _, _ := serviceAccountTestServer(t, &fakeAccountManager{})
	server.cfg.Servers[0].BaseURL = "http://127.0.0.1:8080"
	server.registry = &session.Registry{}
	manager := &fakeHardeningManager{status: hardening.Status{
		Supported: true,
		ACL:       hardening.ComponentStatus{Summary: "15 ACL drift items"},
		Firewall:  hardening.ComponentStatus{Applied: true, Summary: "user-scoped outbound policy verified"},
	}}
	server.SetHardeningManager(manager)

	request := httptest.NewRequest(http.MethodPost, "/api/hardening", strings.NewReader(`{"action":"verify","server_id":"local"}`))
	request.Header.Set("Content-Type", "application/json")
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var body struct {
		OK        bool               `json:"ok"`
		Message   string             `json:"message"`
		Operation hardeningOperation `json:"operation"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.OK || body.Operation.State != "failed" || !strings.Contains(body.Message, "15 ACL drift items") {
		t.Fatalf("drift was not reported as failure: %+v", body)
	}
}
