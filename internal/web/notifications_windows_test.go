//go:build windows

package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"harness/internal/config"
	"harness/internal/credential"
	"harness/internal/events"
	"harness/internal/notifications"
)

type fakeNotificationManager struct {
	state notifications.State
	tests int
}

func (m *fakeNotificationManager) Configure(raw string) error {
	m.state = notifications.State{Configured: raw != ""}
	if raw != "" {
		m.state.Host = "discord.com"
	}
	return nil
}
func (m *fakeNotificationManager) Validate(string) error      { return nil }
func (m *fakeNotificationManager) State() notifications.State { return m.state }
func (m *fakeNotificationManager) SendTest(context.Context) error {
	m.tests++
	return nil
}

func TestNotificationEndpointStoresOnlyDPAPICredentialAndSupportsTestAndClear(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults(filepath.Join(root, "workspace"))
	configPath := filepath.Join(root, "harness.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	server := New(&cfg, configPath, root, RuntimeRoots{Application: root, Data: root, Workspace: cfg.Workspace}, bus)
	server.operatorRequest = func(*http.Request) error { return nil }
	store, err := credential.NewNamed(root, cfg.Notifications.DiscordCredential)
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeNotificationManager{}
	server.SetNotifications(manager, store)
	secret := "https://discord.com/api/webhooks/123/super-secret-token"

	response := postNotification(t, server, `{"action":"save","url":"`+secret+`"}`)
	if response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte(secret)) {
		t.Fatalf("save status=%d body=%s", response.Code, response.Body)
	}
	plain, err := store.Read()
	if err != nil || string(plain) != secret {
		t.Fatalf("stored=%q err=%v", plain, err)
	}
	configBytes, err := os.ReadFile(configPath)
	if err != nil || bytes.Contains(configBytes, []byte(secret)) || !bytes.Contains(configBytes, []byte(`"discord_credential": "discord-webhook"`)) {
		t.Fatalf("config=%s err=%v", configBytes, err)
	}

	response = postNotification(t, server, `{"action":"test"}`)
	if response.Code != http.StatusOK || manager.tests != 1 {
		t.Fatalf("test status=%d count=%d body=%s", response.Code, manager.tests, response.Body)
	}
	response = postNotification(t, server, `{"action":"clear"}`)
	if response.Code != http.StatusOK || manager.State().Configured || store.Status().Stored {
		t.Fatalf("clear status=%d state=%+v credential=%+v", response.Code, manager.State(), store.Status())
	}
}

func postNotification(t *testing.T, server *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/notifications", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}
