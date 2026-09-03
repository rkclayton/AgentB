//go:build windows

package web

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/credential"
	"harness/internal/events"
	"harness/internal/tools"
)

func TestShellCredentialEndpointNeverReturnsPassword(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults(root)
	store := credential.New(root + `\harness.json`)
	shell := tools.NewShell(cfg.Shell)
	shell.Configure(cfg)
	shell.SetCredentialStore(store)
	server := New(&cfg, root+`\harness.json`, root, events.NewBus())
	server.SetShellSecurity(store, shell)

	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString(random)
	request := httptest.NewRequest(http.MethodPost, "/api/shell-credential", strings.NewReader(`{"action":"store","password":"`+password+`"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("store status=%d body=%s", response.Code, response.Body)
	}
	if bytes.Contains(response.Body.Bytes(), []byte(password)) || !strings.Contains(response.Body.String(), `"stored":true`) {
		t.Fatalf("unsafe or unexpected response: %s", response.Body)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/shell-credential", strings.NewReader(`{"action":"clear"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"stored":false`) {
		t.Fatalf("clear status=%d body=%s", response.Code, response.Body)
	}
}
