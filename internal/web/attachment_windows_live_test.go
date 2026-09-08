//go:build windows

package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"harness/internal/agent"
	"harness/internal/config"
	"harness/internal/credential"
	"harness/internal/events"
	"harness/internal/projection"
	"harness/internal/session"
	"harness/internal/tools"
)

func TestAttachmentIngestLiveServiceSplit(t *testing.T) {
	if os.Getenv("AGENTB_ATTACHMENT_LIVE") != "1" {
		t.Skip("set AGENTB_ATTACHMENT_LIVE=1 for the operator integration test")
	}
	workspaceParent := os.Getenv("AGENTB_ATTACHMENT_LIVE_WORKSPACE_PARENT")
	dataRoot := os.Getenv("AGENTB_ATTACHMENT_LIVE_DATA_ROOT")
	account := os.Getenv("AGENTB_ATTACHMENT_LIVE_ACCOUNT")
	if workspaceParent == "" || dataRoot == "" || account == "" {
		t.Fatal("AGENTB_ATTACHMENT_LIVE_WORKSPACE_PARENT, _DATA_ROOT, and _ACCOUNT are required")
	}
	workspace, err := os.MkdirTemp(workspaceParent, "agentb-2g-live-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if strings.HasPrefix(filepath.Base(workspace), "agentb-2g-live-") {
			if err := os.RemoveAll(workspace); err != nil {
				t.Errorf("remove disposable workspace: %v", err)
			}
		}
	})

	var requests atomic.Int32
	var sawLabels atomic.Bool
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		encoded, _ := json.Marshal(body["messages"])
		attempt := requests.Add(1)
		if attempt == 1 {
			text := string(encoded)
			sawLabels.Store(strings.Contains(text, "attached: attachments/note.txt") && strings.Contains(text, "attachments/brief.docx.txt") && strings.Contains(text, "binary — this profile cannot read it"))
			writeLiveModelChunk(w, map[string]any{
				"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call-read", "type": "function", "function": map[string]any{"name": "read_file", "arguments": `{"path":"attachments/note.txt"}`}}}}, "finish_reason": "tool_calls"}},
				"usage":   map[string]any{"prompt_tokens": 60, "completion_tokens": 10},
			})
			return
		}
		writeLiveModelChunk(w, map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{"content": "I read the attachment text."}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 80, "completion_tokens": 8},
		})
	}))
	defer model.Close()

	cfg := config.Defaults(workspace)
	cfg.Context.Accounting = "estimated"
	cfg.Shell.ServiceAccount = config.ShellServiceAccount{Enabled: true, Account: account, Domain: "."}
	cfg.Servers[0].ID = "live"
	cfg.Servers[0].Model = "fake-model"
	cfg.Servers[0].BaseURL = model.URL
	cfg.Servers[0].Context.NCtx = 32768
	cfg.Servers[0].Capabilities.Streaming = true
	cfg.Servers[0].Capabilities.ToolCalls = true
	cfg.Servers[0].Capabilities.OverflowBehavior = "error"
	cfg.Agents = []config.Agent{{Name: "Live", B: "live", Toolset: config.FullToolset()}}

	temporary := t.TempDir()
	writers, err := events.NewWriters(filepath.Join(temporary, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	defer writers.Close()
	bus := events.NewBus()
	projector := projection.NewStore()
	bus.SetDurableSink(writers.WriteRecord, projector.Apply, projector.MarkStale)
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	server := New(&cfg, filepath.Join(temporary, "harness.json"), filepath.Join(root, "web"), RuntimeRoots{Application: root, Data: temporary, Workspace: workspace}, bus)
	server.SetProjection(projector, writers)
	registry := session.NewRegistry(bus, writers, server.Profile, cfg.Run.MaxTurns, server.ConfigSnapshot)
	server.SetRegistry(registry)
	credentialStore := credential.New(dataRoot)
	fileIdentity := tools.NewFileIdentity(credentialStore)
	fileIdentity.Configure(cfg)
	toolRegistry := tools.New(fileIdentity.Wrap(tools.NewReadFile(cfg.Tools.ReadFile)))
	renderer, err := agent.LoadTemplate(filepath.Join(root, "prompts", "system.md"))
	if err != nil {
		t.Fatal(err)
	}
	runner := agent.NewRunner(bus, toolRegistry, renderer, server.Profile, server.ConfigSnapshot)
	scheduler := agent.NewScheduler(runner, registry, bus, server.ConfigSnapshot)
	server.SetRuntime(scheduler, runner, renderer)
	item, err := registry.Create("main", "live", workspace)
	if err != nil {
		t.Fatal(err)
	}
	runner.PublishBudget(context.Background(), item)

	instance := httptest.NewServer(server.Handler())
	defer instance.Close()
	if strings.Contains(instance.URL, ":7337") || strings.Contains(instance.URL, ":8790") {
		t.Fatalf("reserved port: %s", instance.URL)
	}
	textResult := postLiveAttachment(t, instance.URL, server.mutationToken, "note.txt", []byte("live attachment text"))
	binaryResult := postLiveAttachment(t, instance.URL, server.mutationToken, "payload.bin", []byte{0, 1, 2, 3})
	docxResult := postLiveAttachment(t, instance.URL, server.mutationToken, "brief.docx", liveDOCX(t, "live docx words"))
	eventsCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	postLiveJSON(t, instance.URL+"/api/message", server.mutationToken, map[string]any{
		"session_id": "main", "text": "Read these files.", "attachments": []events.Attachment{textResult.Attachment, binaryResult.Attachment, docxResult.Attachment},
	}, http.StatusAccepted)

	readOK := false
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-eventsCh:
			if event.Type == events.ToolResult {
				data, _ := event.Data.(map[string]any)
				readOK = data["name"] == "read_file" && data["ok"] == true && data["operator_context"] == false
			}
			if event.Type == events.RunStopped {
				if !readOK || !sawLabels.Load() || requests.Load() != 2 {
					t.Fatalf("read_ok=%v labels=%v requests=%d", readOK, sawLabels.Load(), requests.Load())
				}
				sidecar, err := os.ReadFile(filepath.Join(workspace, "attachments", "brief.docx.txt"))
				if err != nil || !strings.Contains(string(sidecar), "live docx words") {
					t.Fatalf("sidecar=%q err=%v", sidecar, err)
				}
				t.Logf("instance=%s text_tier=%s binary_tier=%s docx_tier=%s", instance.URL, textResult.Tier, binaryResult.Tier, docxResult.Tier)
				return
			}
		case <-deadline.C:
			t.Fatal("timed out waiting for attachment run")
		}
	}
}

func postLiveAttachment(t *testing.T, baseURL, token, name string, data []byte) attachmentResponse {
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
	request, _ := http.NewRequest(http.MethodPost, baseURL+"/api/attachments", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-AgentB-Mutation-Token", token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("upload %s status=%d body=%s", name, response.StatusCode, payload)
	}
	var result attachmentResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func liveDOCX(t *testing.T, text string) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	part, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, `<w:document xmlns:w="urn:w"><w:p><w:r><w:t>`+text+`</w:t></w:r></w:p></w:document>`)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}
