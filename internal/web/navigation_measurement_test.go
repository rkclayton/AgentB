package web

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
)

func TestNavigationMeasurementWritesOneSessionTapeEvent(t *testing.T) {
	root := t.TempDir()
	writers, err := events.NewWriters(filepath.Join(root, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writers.Close() })
	path, err := writers.OpenSession("s1")
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	bus.SetSink(writers.Write)
	cfg := config.Defaults(root)
	server := New(&cfg, filepath.Join(root, "harness.json"), root, RuntimeRoots{Data: root, Workspace: root}, bus)
	body := `{"navigation_id":"nav-1","navigation_kind":"flip","from":"console","to":"chat","document_request_parse_ms":12.5,"module_page_init_ms":1.25,"session_state_fetch_ms":0,"event_stream_connect_ms":2,"transcript_surface_rebuild_paint_ms":30,"end_to_end_ms":45,"transcript_entries":400,"chat_id":"s1","model_reachability":"reachable","since_previous_navigation_ms":1200,"instrumentation_sync_ms":0.04}`
	for range 2 {
		response := httptest.NewRecorder()
		server.navigationMeasurement(response, httptest.NewRequest(http.MethodPost, "/api/navigation-measurements", strings.NewReader(body)))
		if response.Code != http.StatusNoContent {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
	file, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	scanner := bufio.NewScanner(opened)
	var got []events.Event
	for scanner.Scan() {
		var event events.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		got = append(got, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Type != events.NavigationMeasured || got[0].SessionID != "s1" {
		t.Fatalf("events=%+v", got)
	}
	encoded, _ := json.Marshal(got[0].Data)
	if !strings.Contains(string(encoded), `"transcript_entries":400`) || !strings.Contains(string(encoded), `"since_previous_navigation_ms":1200`) {
		t.Fatalf("data=%s", encoded)
	}
}

func TestNavigationStartWritesWithoutCompletionAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	writers, err := events.NewWriters(filepath.Join(root, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writers.Close() })
	path, err := writers.OpenSession("s1")
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	bus.SetSink(writers.Write)
	_, unsubscribe := bus.Subscribe()
	t.Cleanup(unsubscribe)
	cfg := config.Defaults(root)
	server := New(&cfg, filepath.Join(root, "harness.json"), root, RuntimeRoots{Data: root, Workspace: root}, bus)
	body := `{"navigation_id":"nav-start-1","navigation_kind":"flip","from":"console","to":"chat","full_document":true,"clicked_at":1789090000123.5,"chat_id":"s1","since_previous_navigation_ms":335.9}`
	for range 2 {
		response := httptest.NewRecorder()
		server.navigationStart(response, httptest.NewRequest(http.MethodPost, "/api/navigation-starts", strings.NewReader(body)))
		if response.Code != http.StatusNoContent {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
	opened, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	scanner := bufio.NewScanner(opened)
	var got []events.Event
	for scanner.Scan() {
		var event events.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		got = append(got, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Type != events.NavigationStarted || got[0].SessionID != "s1" {
		t.Fatalf("events=%+v", got)
	}
	encoded, _ := json.Marshal(got[0].Data)
	for _, want := range []string{`"navigation_id":"nav-start-1"`, `"full_document":true`, `"clicked_at":1789090000123.5`, `"subscriber_count":1`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("data=%s missing %s", encoded, want)
		}
	}
}

func TestDocumentRequestRecordsServerPhasesAndConnection(t *testing.T) {
	root := t.TempDir()
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "chat.html"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	writers, err := events.NewWriters(filepath.Join(root, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writers.Close() })
	path, err := writers.OpenSession("s1")
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	bus.SetSink(writers.Write)
	cfg := config.Defaults(root)
	server := New(&cfg, filepath.Join(root, "harness.json"), webDir, RuntimeRoots{Data: root, Workspace: root}, bus)
	request := httptest.NewRequest(http.MethodGet, "/chat?setup=skip&session=s1&navigation_id=nav-document-1", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "hello" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}

	opened, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	scanner := bufio.NewScanner(opened)
	var got []events.Event
	for scanner.Scan() {
		var event events.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		got = append(got, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Type != events.NavigationDocumentStarted || got[1].Type != events.NavigationDocumentCompleted {
		t.Fatalf("events=%+v", got)
	}
	for _, event := range got {
		if event.SessionID != "s1" {
			t.Fatalf("event session=%q", event.SessionID)
		}
		encoded, _ := json.Marshal(event.Data)
		for _, want := range []string{`"navigation_id":"nav-document-1"`, `"request_id":"nav-document-1"`, `"connection_identity":"127.0.0.1:54321"`} {
			if !strings.Contains(string(encoded), want) {
				t.Fatalf("%s data=%s missing %s", event.Type, encoded, want)
			}
		}
	}
	started, _ := json.Marshal(got[0].Data)
	for _, want := range []string{`"arrival_at":`, `"handler_entered_at":`} {
		if !strings.Contains(string(started), want) {
			t.Fatalf("started=%s missing %s", started, want)
		}
	}
	completed, _ := json.Marshal(got[1].Data)
	for _, want := range []string{`"handler_exited_at":`, `"bytes_written":5`} {
		if !strings.Contains(string(completed), want) {
			t.Fatalf("completed=%s missing %s", completed, want)
		}
	}
}

func TestNavigationSuppressionWritesOneSessionTapeEvent(t *testing.T) {
	root := t.TempDir()
	writers, err := events.NewWriters(filepath.Join(root, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writers.Close() })
	path, err := writers.OpenSession("s1")
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	bus.SetSink(writers.Write)
	cfg := config.Defaults(root)
	server := New(&cfg, filepath.Join(root, "harness.json"), root, RuntimeRoots{Data: root, Workspace: root}, bus)
	body := `{"suppression_id":"suppressed-1","navigation_kind":"flip","from":"chat","to":"console","clicked_at":1789090000123.5,"chat_id":"s1"}`
	for range 2 {
		response := httptest.NewRecorder()
		server.navigationSuppression(response, httptest.NewRequest(http.MethodPost, "/api/navigation-suppressions", strings.NewReader(body)))
		if response.Code != http.StatusNoContent {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
	opened, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	scanner := bufio.NewScanner(opened)
	var got []events.Event
	for scanner.Scan() {
		var event events.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		got = append(got, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Type != events.NavigationSuppressed || got[0].SessionID != "s1" {
		t.Fatalf("events=%+v", got)
	}
}

func TestNavigationMeasurementRejectsInvalidPhase(t *testing.T) {
	root := t.TempDir()
	cfg := config.Defaults(root)
	server := New(&cfg, filepath.Join(root, "harness.json"), root, RuntimeRoots{Data: root, Workspace: root}, events.NewBus())
	body := `{"navigation_id":"nav-1","navigation_kind":"flip","from":"console","to":"chat","document_request_parse_ms":-1,"module_page_init_ms":0,"session_state_fetch_ms":0,"event_stream_connect_ms":0,"transcript_surface_rebuild_paint_ms":0,"end_to_end_ms":0,"transcript_entries":0,"chat_id":"","model_reachability":"unknown","since_previous_navigation_ms":null,"instrumentation_sync_ms":0}`
	response := httptest.NewRecorder()
	server.navigationMeasurement(response, httptest.NewRequest(http.MethodPost, "/api/navigation-measurements", strings.NewReader(body)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNavigationMeasurementAcceptsPlanSettingsSource(t *testing.T) {
	body := navigationMeasurementBody{NavigationID: "plan-settings", NavigationKind: "settings", From: "plan", To: "settings", ModelReachability: "unknown"}
	if !validNavigationMeasurement(body) {
		t.Fatal("Plan uses the shared settings cog and is a valid source surface")
	}
}
