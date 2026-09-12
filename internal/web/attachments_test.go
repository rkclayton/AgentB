package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
)

func TestAttachmentsCollisionNumericSuffixAndSHA256Dedupe(t *testing.T) {
	server, workspace := attachmentTestServer(t, nil)
	first := postAttachment(t, server, "report.txt", []byte("first"))
	second := postAttachment(t, server, "report.txt", []byte("second"))
	reused := postAttachment(t, server, "report.txt", []byte("second"))
	if first.Path != "attachments/report.txt" || second.Path != "attachments/report (2).txt" {
		t.Fatalf("collision paths: first=%q second=%q", first.Path, second.Path)
	}
	if first.Kind != "text" || second.Kind != "text" || reused.Kind != "text" {
		t.Fatalf("classification missing from upload response: first=%q second=%q reused=%q", first.Kind, second.Kind, reused.Kind)
	}
	if reused.Path != second.Path || !reused.Reused || !strings.Contains(reused.Note, "identical attachment reused") {
		t.Fatalf("dedupe=%+v", reused)
	}
	data, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(second.Path)))
	if err != nil || string(data) != "second" {
		t.Fatalf("stored second=%q, %v", data, err)
	}
}

func TestAttachmentsRefusesOversizeAndZIP(t *testing.T) {
	server, _ := attachmentTestServer(t, func(cfg *config.Config) { cfg.Tools.Attachments.MaxBytes = 4 })
	for _, item := range []struct {
		name string
		data []byte
		want int
	}{
		{"large.txt", []byte("12345"), http.StatusRequestEntityTooLarge},
		{"archive.zip", []byte("PK"), http.StatusBadRequest},
	} {
		request := multipartAttachmentRequest(t, server, item.name, item.data)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != item.want {
			t.Fatalf("%s status=%d body=%s", item.name, response.Code, response.Body)
		}
	}
}

func TestAttachmentsRequiresMutationToken(t *testing.T) {
	server, _ := attachmentTestServer(t, nil)
	request := multipartAttachmentRequest(t, server, "note.txt", []byte("hello"))
	request.Header.Del("X-AgentB-Mutation-Token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestAttachmentsDOCXCreatesReadableSidecar(t *testing.T) {
	server, workspace := attachmentTestServer(t, nil)
	var document bytes.Buffer
	writer := zip.NewWriter(&document)
	part, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, `<w:document xmlns:w="urn:w"><w:p><w:r><w:t>Attachment words</w:t></w:r></w:p></w:document>`)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	result := postAttachment(t, server, "brief.docx", document.Bytes())
	if result.Tier != "office" || result.Sidecar != "attachments/brief.docx.txt" {
		t.Fatalf("result=%+v", result)
	}
	text, err := os.ReadFile(filepath.Join(workspace, "attachments", "brief.docx.txt"))
	if err != nil || !strings.Contains(string(text), "Attachment words") {
		t.Fatalf("sidecar=%q, %v", text, err)
	}
}

func TestAttachmentsPDFExtractionEndpointStoresUntrustedSidecar(t *testing.T) {
	extractor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s", r.Method)
		}
		_, _ = io.WriteString(w, "extracted PDF text")
	}))
	defer extractor.Close()
	server, workspace := attachmentTestServer(t, func(cfg *config.Config) { cfg.Servers[0].ExtractURL = extractor.URL })
	result := postAttachment(t, server, "paper.pdf", []byte("%PDF-1.1\n%%EOF\n"))
	if result.Tier != "extracted" || !strings.Contains(result.Note, "untrusted") {
		t.Fatalf("result=%+v", result)
	}
	text, err := os.ReadFile(filepath.Join(workspace, "attachments", "paper.pdf.txt"))
	if err != nil || !strings.Contains(string(text), "untrusted: true") || !strings.Contains(string(text), "extracted PDF text") {
		t.Fatalf("sidecar=%q, %v", text, err)
	}
}

func TestMessageAttachmentMetadataMustMatchWorkspaceFile(t *testing.T) {
	server, _ := attachmentTestServer(t, nil)
	uploaded := postAttachment(t, server, "note.txt", []byte("hello"))
	uploaded.Bytes++
	body, _ := json.Marshal(map[string]any{"session_id": "main", "text": "read it", "attachments": []any{uploaded.Attachment}})
	request := httptest.NewRequest(http.MethodPost, "/api/message", bytes.NewReader(body))
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "metadata does not match") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestExchangeFilesEnumeratesOnlyTopLevelRegularFiles(t *testing.T) {
	exchange := t.TempDir()
	_ = os.WriteFile(filepath.Join(exchange, "ready.txt"), []byte("ready"), 0o600)
	_ = os.Mkdir(filepath.Join(exchange, "nested"), 0o700)
	_ = os.WriteFile(filepath.Join(exchange, "nested", "hidden.txt"), []byte("hidden"), 0o600)
	server, _ := attachmentTestServer(t, func(cfg *config.Config) { cfg.Deliver.ExchangeFolder = exchange })
	request := httptest.NewRequest(http.MethodGet, "/api/exchange-files", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "ready.txt") || strings.Contains(response.Body.String(), "hidden.txt") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	escape := httptest.NewRequest(http.MethodGet, "/api/exchange-files?path=nested%2Fhidden.txt", nil)
	refused := httptest.NewRecorder()
	server.Handler().ServeHTTP(refused, escape)
	if refused.Code != http.StatusNotFound {
		t.Fatalf("nested status=%d body=%s", refused.Code, refused.Body)
	}
}

func attachmentTestServer(t *testing.T, modify func(*config.Config)) (*Server, string) {
	t.Helper()
	workspace := t.TempDir()
	cfg := config.Defaults(workspace)
	cfg.Servers[0].Model = "test"
	cfg.Servers[0].Context.NCtx = 32768
	cfg.Servers[0].Capabilities.Streaming = true
	cfg.Servers[0].Capabilities.ToolCalls = true
	cfg.Servers[0].Capabilities.OverflowBehavior = "error"
	if modify != nil {
		modify(&cfg)
	}
	data := t.TempDir()
	writers, err := events.NewWriters(filepath.Join(data, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writers.Close() })
	bus := events.NewBus()
	server := New(&cfg, filepath.Join(data, "harness.json"), t.TempDir(), RuntimeRoots{Workspace: workspace}, bus)
	registry := session.NewRegistry(bus, writers, server.Profile, cfg.Run.MaxTurns, server.ConfigSnapshot)
	server.SetRegistry(registry)
	if _, err := registry.Create("main", cfg.Servers[0].ID, workspace); err != nil {
		t.Fatal(err)
	}
	return server, workspace
}

func postAttachment(t *testing.T, server *Server, name string, data []byte) attachmentResponse {
	t.Helper()
	request := multipartAttachmentRequest(t, server, name, data)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("upload %s status=%d body=%s", name, response.Code, response.Body)
	}
	var result attachmentResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func multipartAttachmentRequest(t *testing.T, server *Server, name string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("session_id", "main")
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(data)
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/attachments", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	authorizeMutation(request, server)
	return request
}
