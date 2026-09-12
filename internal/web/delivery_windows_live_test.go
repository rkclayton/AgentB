//go:build windows

package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	"harness/internal/delivery"
	"harness/internal/events"
	"harness/internal/projection"
	"harness/internal/session"
	"harness/internal/tools"
)

// TestDeliveryLiveServiceSplit is operator-gated because it uses the installed
// service-account credential and an ACL-prepared workspace parent. The model
// and Agent_b HTTP servers both use disposable loopback ports.
func TestDeliveryLiveServiceSplit(t *testing.T) {
	if os.Getenv("AGENTB_DELIVERY_LIVE") != "1" {
		t.Skip("set AGENTB_DELIVERY_LIVE=1 for the operator integration test")
	}
	workspaceParent := os.Getenv("AGENTB_DELIVERY_LIVE_WORKSPACE_PARENT")
	dataRoot := os.Getenv("AGENTB_DELIVERY_LIVE_DATA_ROOT")
	account := os.Getenv("AGENTB_DELIVERY_LIVE_ACCOUNT")
	if workspaceParent == "" || dataRoot == "" || account == "" {
		t.Fatal("AGENTB_DELIVERY_LIVE_WORKSPACE_PARENT, _DATA_ROOT, and _ACCOUNT are required")
	}
	workspace, err := os.MkdirTemp(workspaceParent, "agentb-2n-live-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if base := filepath.Base(workspace); strings.HasPrefix(base, "agentb-2n-live-") {
			if err := os.RemoveAll(workspace); err != nil {
				t.Errorf("remove disposable workspace: %v", err)
			}
		}
	})
	exchange := t.TempDir()

	var modelRequests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		attempt := modelRequests.Add(1)
		if attempt == 1 {
			writeLiveModelChunk(w, map[string]any{
				"choices": []any{map[string]any{
					"delta": map[string]any{"tool_calls": []any{
						map[string]any{"index": 0, "id": "call-write", "type": "function", "function": map[string]any{"name": "write_file", "arguments": `{"path":"written.txt","content":"write-file bytes"}`}},
						map[string]any{"index": 1, "id": "call-script", "type": "function", "function": map[string]any{"name": "run_script", "arguments": `{"language":"powershell","source":"[IO.File]::WriteAllText('script-output.txt', 'run-script bytes')"}`}},
					}},
					"finish_reason": "tool_calls",
				}},
				"usage": map[string]any{"prompt_tokens": 40, "completion_tokens": 20},
			})
			return
		}
		writeLiveModelChunk(w, map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{"content": "Files are ready."}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 70, "completion_tokens": 4},
		})
	}))
	defer model.Close()

	cfg := config.Defaults(workspace)
	cfg.Context.Accounting = "estimated"
	cfg.Deliver.Mode = config.DeliverModeBoth
	cfg.Deliver.ExchangeFolder = exchange
	cfg.Shell.FileRoutingGuard = liveBool(false)
	cfg.Shell.ServiceAccount = config.ShellServiceAccount{Enabled: true, Account: account, Domain: "."}
	cfg.Servers[0].ID = "live"
	cfg.Servers[0].Model = "fake-model"
	cfg.Servers[0].BaseURL = model.URL
	cfg.Servers[0].Context.NCtx = 32768
	cfg.Servers[0].Context.ReserveOutput = 4096
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

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	server := New(&cfg, filepath.Join(temporary, "harness.json"), filepath.Join(root, "web"), RuntimeRoots{
		Application: root, Data: temporary, Workspace: workspace,
	}, bus)
	server.SetProjection(projector, writers)
	registry := session.NewRegistry(bus, writers, server.Profile, cfg.Run.MaxTurns, server.ConfigSnapshot)
	server.SetRegistry(registry)

	workspaces := session.NewWorkspaceRegistry()
	coordinator := tools.NewFileCoordinator(workspaces, registry.Label, bus)
	credentialStore := credential.New(dataRoot)
	fileIdentity := tools.NewFileIdentity(credentialStore)
	fileIdentity.Configure(cfg)
	shell := tools.NewShell(cfg.Shell)
	shell.SetFileCoordinator(coordinator)
	shell.Configure(cfg)
	shell.SetCredentialStore(credentialStore)
	server.SetShellSecurity(credentialStore, shell)
	toolRegistry := tools.New(
		fileIdentity.Wrap(tools.NewWriteFile(coordinator)),
		tools.NewRunScript(shell),
	)
	renderer, err := agent.LoadTemplate(filepath.Join(root, "prompts", "system.md"))
	if err != nil {
		t.Fatal(err)
	}
	runner := agent.NewRunner(bus, toolRegistry, renderer, server.Profile, server.ConfigSnapshot)
	deliveryManager := delivery.New(bus, server.ConfigSnapshot)
	runner.SetDeliverer(func(item *session.Session, runID string, files []delivery.Source) delivery.Result {
		return deliveryManager.Deliver(item, runID, files)
	})
	scheduler := agent.NewScheduler(runner, registry, bus, server.ConfigSnapshot)
	server.SetRuntime(scheduler, runner, renderer)
	item, err := registry.Create("main", "live", workspace)
	if err != nil {
		t.Fatal(err)
	}
	runner.PublishBudget(context.Background(), item)

	eventCh, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	instance := httptest.NewServer(server.Handler())
	defer instance.Close()
	if strings.Contains(instance.URL, ":7337") || strings.Contains(instance.URL, ":8790") {
		t.Fatalf("disposable instance used a reserved port: %s", instance.URL)
	}
	postLiveJSON(t, instance.URL+"/api/message", server.mutationToken, map[string]any{
		"session_id": "main", "text": "Create both live verification files.",
	}, http.StatusAccepted)

	var delivered delivery.Result
	writeResultWithFile := false
	scriptResultWithFile := false
	approved := false
	stopped := false
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	for !stopped {
		select {
		case event := <-eventCh:
			switch event.Type {
			case events.ApprovalRequired:
				data, _ := event.Data.(map[string]any)
				if data["call_id"] == "call-script" {
					postLiveJSON(t, instance.URL+"/api/approve", server.mutationToken, map[string]any{
						"session_id": "main", "call_id": "call-script", "decision": "approve",
					}, http.StatusOK)
					approved = true
				}
			case events.ToolResult:
				data, _ := event.Data.(map[string]any)
				_, hasFile := data["file"]
				if data["name"] == "write_file" {
					writeResultWithFile = hasFile
				}
				if data["name"] == "run_script" {
					scriptResultWithFile = hasFile
				}
			case events.FilesDelivered:
				delivered, _ = event.Data.(delivery.Result)
			case events.RunStopped:
				stopped = true
			}
		case <-deadline.C:
			t.Fatal("timed out waiting for the disposable run")
		}
	}
	if !approved || !writeResultWithFile || scriptResultWithFile {
		t.Fatalf("approval=%v write chip source=%v script chip source=%v", approved, writeResultWithFile, scriptResultWithFile)
	}
	if modelRequests.Load() != 2 {
		t.Fatalf("model requests=%d, want 2", modelRequests.Load())
	}
	if len(delivered.Items) != 1 || delivered.Items[0].SourcePath != "written.txt" || delivered.Items[0].Status != "copied" {
		t.Fatalf("delivery=%+v", delivered)
	}

	stateResponse, err := http.Get(instance.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer stateResponse.Body.Close()
	var state struct {
		Sessions map[string]projection.Snapshot `json:"sessions"`
	}
	if err := json.NewDecoder(stateResponse.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	chipSources := 0
	for _, entry := range state.Sessions["main"].Chat {
		if entry.Type == "tool" && entry.Result != nil {
			if _, ok := entry.Result["file"]; ok {
				chipSources++
			}
		}
	}
	if chipSources != 1 {
		t.Fatalf("projected chip sources=%d, want 1", chipSources)
	}

	download, err := http.Get(instance.URL + "/api/files/written.txt?session=main")
	if err != nil {
		t.Fatal(err)
	}
	downloaded, readErr := io.ReadAll(download.Body)
	download.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if download.StatusCode != http.StatusOK || !strings.HasPrefix(download.Header.Get("Content-Disposition"), "attachment;") {
		t.Fatalf("download status=%d disposition=%q", download.StatusCode, download.Header.Get("Content-Disposition"))
	}
	want := []byte("write-file bytes")
	if !bytes.Equal(downloaded, want) || liveSHA256(downloaded) != liveSHA256(want) {
		t.Fatalf("download bytes=%q sha256=%s", downloaded, liveSHA256(downloaded))
	}
	exchanged, err := os.ReadFile(filepath.Join(exchange, "written.txt"))
	if err != nil || !bytes.Equal(exchanged, want) {
		t.Fatalf("exchange bytes=%q err=%v", exchanged, err)
	}
	scripted, err := os.ReadFile(filepath.Join(workspace, "script-output.txt"))
	if err != nil || string(scripted) != "run-script bytes" {
		t.Fatalf("run_script bytes=%q err=%v", scripted, err)
	}
	if _, err := os.Stat(filepath.Join(exchange, "script-output.txt")); !os.IsNotExist(err) {
		t.Fatalf("run_script output was unexpectedly tracked into exchange: %v", err)
	}
	t.Logf("instance=%s download_sha256=%s exchange=%s", instance.URL, liveSHA256(downloaded), exchange)
}

func writeLiveModelChunk(w http.ResponseWriter, value any) {
	encoded, _ := json.Marshal(value)
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
}

func postLiveJSON(t *testing.T, url, token string, value any, wantStatus int) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-AgentB-Mutation-Token", token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("POST %s status=%d body=%s", url, response.StatusCode, body)
	}
}

func liveSHA256(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func liveBool(value bool) *bool { return &value }
