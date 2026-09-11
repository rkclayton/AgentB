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
