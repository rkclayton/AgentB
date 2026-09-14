package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"harness/internal/buildinfo"
	"harness/internal/credential"
	"harness/internal/events"
	"harness/internal/projection"
	"harness/internal/tools"
)

func (s *Server) snapshot() map[string]any {
	if s.replay != nil {
		return s.snapshotWithSessions(s.replay.Sessions, true)
	}
	if s.projector != nil && s.writers != nil {
		sessions, err := s.projector.Snapshot(s.writers.SessionCursors())
		if err == nil {
			return s.snapshotWithSessions(sessions, false)
		}
		result := s.snapshotWithSessions(map[string]projection.Snapshot{}, false)
		result["projection_error"] = err.Error()
		return result
	}
	return s.snapshotWithSessions(map[string]projection.Snapshot{}, false)
}
func (s *Server) snapshotWithSessions(sessions any, replay bool) map[string]any {
	masked := s.ConfigSnapshot().Masked()
	describedShell := tools.NewShell(masked.Shell)
	describedShell.Configure(masked)
	shellDescription := describedShell.Description()
	credentialStatus := credential.Status{}
	identityStatus := tools.ShellIdentityStatus{}
	sandboxStatus := tools.SandboxStatus{Reason: "sandbox runtime is unavailable"}
	if s.credential != nil {
		credentialStatus = s.credential.Status()
	}
	if s.shell != nil {
		identityStatus = s.shell.IdentityStatus()
		sandboxStatus = s.shell.SandboxStatus()
	}
	return map[string]any{
		"sessions": sessions, "servers": masked.Servers, "config": masked, "replay": replay,
		"agent_server_changes": s.agentServerChanges(),
		"build":                buildinfo.Current(),
		"signature":            s.signingState(),
		"mutation_token":       s.mutationToken, "shell_credential": credentialStatus, "shell_identity": identityStatus, "sandbox": sandboxStatus,
		"serving_facts": servingFacts(filepath.Join(s.roots.Application, "SERVING.md")),
		"flow":          map[string]any{"stages": events.Stages, "edges": [][2]string{{"assemble", "call_model"}, {"call_model", "parse"}, {"parse", "dispatch"}, {"dispatch", "execute"}, {"execute", "append"}, {"append", "assemble"}}},
		"tools": []map[string]string{
			{"name": "read_file", "description": "Read numbered local UTF-8 text by byte offset and limit. When more is true, pass returned next_offset as offset to advance. Unlike fetch_url, it reads the filesystem."},
			{"name": "list_dir", "description": "List entries under local directory path to depth. Unlike find_files, it enumerates contents without a filename pattern."},
			{"name": "write_file", "description": "Create or fully replace the file at path with content, creating parent directories. Unlike edit_file, it writes the whole file."},
			{"name": "edit_file", "description": "Replace one exact, unique old_string in path with new_string. Unlike write_file, it avoids reproducing the whole file."},
			{"name": "search_text", "description": "Search local file contents under path for pattern, optionally filtering filenames with glob. Unlike find_files, it returns matching text lines."},
			{"name": "shell", "description": shellDescription},
			{"name": "remember", "description": "Save note as durable folder memory for future sessions. Call recall first to avoid duplicates; unlike recall, remember writes."},
			{"name": "recall", "description": "Read all durable folder notes; takes no arguments. Use before remember to avoid duplicates; unlike recall, remember never writes."},
			{"name": "fetch_url", "description": "Fetch untrusted public HTTP(S) text by byte offset and limit. When more is true, pass returned next_offset as offset to advance. Unlike read_file, it uses the network."},
			{"name": "find_files", "description": "Find local files under path whose names or relative paths match pattern. Unlike search_text, it does not inspect file contents."},
			{"name": "run_script", "description": tools.NewRunScript(tools.NewShell(masked.Shell)).Description()},
			{"name": "call_service", "description": tools.NewCallService(masked.Services).Description()},
		},
	}
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w)
		return
	}
	writeJSON(w, 200, s.snapshot())
}
func (s *Server) sse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	if s.replay != nil {
		s.replaySSE(w, r, flusher)
		return
	}
	if s.projector != nil && s.writers != nil {
		s.projectionSSE(w, r, flusher)
		return
	}
	ch, unsubscribe := s.bus.Subscribe()
	defer unsubscribe()
	s.writeFrame(w, events.New(events.Snapshot, "", "", s.snapshot()))
	flusher.Flush()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return
			}
			if event.SessionID != "" {
				continue
			}
			s.writeFrame(w, event)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) projectionSSE(w http.ResponseWriter, r *http.Request, flusher http.Flusher) {
	raw, unsubscribeRaw := s.bus.Subscribe()
	defer unsubscribeRaw()
	sessions, patches, unsubscribeProjection, err := s.projector.SubscribeSnapshot(s.writers.SessionCursors())
	if err != nil {
		s.writeFrame(w, events.New(events.Error, "", "", map[string]any{"where": "projection", "message": err.Error()}))
		flusher.Flush()
		return
	}
	defer unsubscribeProjection()
	s.writeFrame(w, events.New(events.Snapshot, "", "", s.snapshotWithSessions(sessions, false)))
	flusher.Flush()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case patch, ok := <-patches:
			if !ok {
				return
			}
			s.writeFrame(w, events.New(events.ProjectionPatch, patch.SessionID, "", patch))
			flusher.Flush()
		case event, ok := <-raw:
			if !ok {
				return
			}
			if event.SessionID != "" {
				continue
			}
			s.writeFrame(w, event)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) replaySSE(w http.ResponseWriter, r *http.Request, flusher http.Flusher) {
	s.writeFrame(w, events.New(events.Snapshot, "", "", s.snapshotWithSessions(s.replay.Initial, true)))
	flusher.Flush()
	instant := r.URL.Query().Get("instant") == "1"
	for _, recorded := range s.replay.Patches {
		if !instant {
			timer := time.NewTimer(20 * time.Millisecond)
			select {
			case <-timer.C:
			case <-r.Context().Done():
				timer.Stop()
				return
			}
		}
		s.writeFrame(w, events.New(events.ProjectionPatch, recorded.Patch.SessionID, "", recorded.Patch))
		flusher.Flush()
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
func (s *Server) writeFrame(w http.ResponseWriter, event events.Event) {
	event.Body = nil
	event.Raw = nil
	data, _ := json.Marshal(event)
	if event.Type == events.ProjectionPatch || event.Type == events.Snapshot {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\nid: %d\n\n", event.Type, data, event.Seq)
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	s.instrumentDocument(w, r, s.pageContent)
}

func (s *Server) pageContent(w http.ResponseWriter, r *http.Request) {
	name := "index.html"
	if r.URL.Path == "/setup" {
		name = "setup.html"
	} else if r.URL.Path == "/chat" {
		name = "index.html"
	} else if r.URL.Path == "/plan" {
		name = "plan.html"
	} else if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path != "/setup" && s.replay == nil && r.URL.Query().Get("setup") != "skip" {
		s.mu.RLock()
		firstRun := len(s.cfg.Servers) == 0
		s.mu.RUnlock()
		if firstRun {
			http.Redirect(w, r, "/setup", http.StatusTemporaryRedirect)
			return
		}
	}
	path := filepath.Join(s.webDir, name)
	if data, err := os.ReadFile(path); err == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, "<!doctype html><title>Agent_b</title><p>Agent_b API is running.</p>")
}

func (s *Server) localDetection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w)
		return
	}
	if s.detectLocal == nil {
		writeError(w, http.StatusNotImplemented, "local detection is unavailable", "detection")
		return
	}
	s.mu.RLock()
	account := s.cfg.Shell.ServiceAccount.Account
	s.mu.RUnlock()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	report, err := s.detectLocal(ctx, account)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "detection")
		return
	}
	writeJSON(w, http.StatusOK, report)
}
