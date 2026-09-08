package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/operatorfiles"
)

func operatorFileServer(t *testing.T) (*Server, *operatorfiles.Manager) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Defaults(filepath.Join(root, "workspace"))
	bus := events.NewBus()
	server := New(&cfg, filepath.Join(root, "harness.json"), root, RuntimeRoots{Data: root, Workspace: cfg.Workspace}, bus)
	manager := operatorfiles.New(root, filepath.Join(root, "logs"), server.ConfigSnapshot)
	if err := manager.Ensure(); err != nil {
		t.Fatal(err)
	}
	server.SetOperatorFiles(manager)
	return server, manager
}

func TestOperatorFileStatusAndConfirmedAttachmentEmpty(t *testing.T) {
	server, manager := operatorFileServer(t)
	attachment := filepath.Join(manager.AttachmentsPath(), "note.txt")
	if err := os.WriteFile(attachment, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	status := httptest.NewRecorder()
	server.operatorFileState(status, httptest.NewRequest(http.MethodGet, "/api/operator-files", nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"attachment_files":1`) || !strings.Contains(status.Body.String(), `"attachment_bytes":5`) {
		t.Fatalf("status=%d body=%s", status.Code, status.Body.String())
	}
	refused := httptest.NewRecorder()
	server.operatorFileState(refused, httptest.NewRequest(http.MethodPost, "/api/operator-files", strings.NewReader(`{"action":"empty_attachments"}`)))
	if refused.Code != http.StatusBadRequest {
		t.Fatalf("refused=%d body=%s", refused.Code, refused.Body.String())
	}
	confirmed := httptest.NewRecorder()
	server.operatorFileState(confirmed, httptest.NewRequest(http.MethodPost, "/api/operator-files", strings.NewReader(`{"action":"empty_attachments","confirm":true}`)))
	if confirmed.Code != http.StatusOK {
		t.Fatalf("confirmed=%d body=%s", confirmed.Code, confirmed.Body.String())
	}
	if _, err := os.Stat(attachment); !os.IsNotExist(err) {
		t.Fatalf("attachment retained: %v", err)
	}
}

func TestOperatorAttachmentSourceListsAndReadsFiles(t *testing.T) {
	server, manager := operatorFileServer(t)
	if err := os.WriteFile(filepath.Join(manager.AttachmentsPath(), "note.txt"), []byte("phone note"), 0o600); err != nil {
		t.Fatal(err)
	}
	listed := httptest.NewRecorder()
	server.operatorAttachments(listed, httptest.NewRequest(http.MethodGet, "/api/operator-attachments", nil))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"path":"note.txt"`) {
		t.Fatalf("listed=%d body=%s", listed.Code, listed.Body.String())
	}
	read := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/operator-attachments?path=note.txt", nil)
	server.operatorAttachments(read, request)
	if read.Code != http.StatusOK || read.Body.String() != "phone note" {
		t.Fatalf("read=%d body=%q", read.Code, read.Body.String())
	}
}

func TestUIErrorRelayPublishesSessionScopedHarnessEvent(t *testing.T) {
	server, _ := operatorFileServer(t)
	channel, cancel := server.bus.Subscribe()
	defer cancel()
	body := strings.NewReader(`{"session_id":"s1","kind":"unhandledrejection","message":"render failed","stack":"at render"}`)
	response := httptest.NewRecorder()
	server.uiError(response, httptest.NewRequest(http.MethodPost, "/api/ui-errors", body))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	select {
	case event := <-channel:
		data, _ := json.Marshal(event.Data)
		if event.Type != events.Error || event.SessionID != "s1" || !strings.Contains(string(data), `"where":"ui"`) || !strings.Contains(string(data), "render failed") {
			t.Fatalf("event=%+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("UI error event not published")
	}
}
