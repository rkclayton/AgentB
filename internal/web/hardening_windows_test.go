//go:build windows

package web

import (
	"context"
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
}

func (m *fakeHardeningManager) Status(context.Context, hardening.Request) (hardening.Status, error) {
	return m.status, nil
}
func (m *fakeHardeningManager) Run(_ context.Context, action string, request hardening.Request) (hardening.RunResult, error) {
	m.runCalls++
	m.runAction, m.runRequest = action, request
	return hardening.RunResult{Attempted: true}, nil
}

func TestHardeningEndpointAppliesOnlyAfterVerifiedIdentity(t *testing.T) {
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

	request := httptest.NewRequest(http.MethodPost, "/api/hardening", strings.NewReader(`{"action":"apply","server_id":"local"}`))
	request.Header.Set("Content-Type", "application/json")
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || manager.runCalls != 1 || manager.runAction != "apply" {
		t.Fatalf("status=%d calls=%d action=%q body=%s", response.Code, manager.runCalls, manager.runAction, response.Body)
	}
	if manager.runRequest.ModelAddress != "198.51.100.10" || manager.runRequest.ModelPort != 8080 || manager.runRequest.AccountName != "agentb-svc" {
		t.Fatalf("unexpected hardening request: %+v", manager.runRequest)
	}
}

func TestHardeningEndpointRejectsUnverifiedIdentity(t *testing.T) {
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
