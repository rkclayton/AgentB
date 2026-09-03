package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"harness/internal/agent"
	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/probe"
	"harness/internal/session"
)

type Server struct {
	mu         sync.RWMutex
	cfg        *config.Config
	configPath string
	bus        *events.Bus
	registry   *session.Registry
	webDir     string
	scheduler  *agent.Scheduler
	runner     *agent.Runner
	prompt     *agent.PromptRenderer
}

func New(cfg *config.Config, path, webDir string, bus *events.Bus) *Server {
	return &Server{cfg: cfg, configPath: path, webDir: webDir, bus: bus}
}
func (s *Server) SetRegistry(registry *session.Registry) { s.registry = registry }
func (s *Server) SetRuntime(scheduler *agent.Scheduler, runner *agent.Runner, prompt *agent.PromptRenderer) {
	s.scheduler = scheduler
	s.runner = runner
	s.prompt = prompt
}
func (s *Server) ConfigSnapshot() config.Config { s.mu.RLock(); defer s.mu.RUnlock(); return *s.cfg }
func (s *Server) Profile(id string) (*config.Profile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.cfg.Servers {
		if s.cfg.Servers[i].ID == id {
			copy := s.cfg.Servers[i]
			return &copy, true
		}
	}
	return nil, false
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.page)
	mux.HandleFunc("/chat", s.page)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(s.webDir))))
	mux.HandleFunc("/api/events", s.sse)
	mux.HandleFunc("/api/state", s.state)
	mux.HandleFunc("/api/sessions", s.sessions)
	mux.HandleFunc("/api/sessions/", s.session)
	mux.HandleFunc("/api/servers", s.servers)
	mux.HandleFunc("/api/servers/", s.server)
	mux.HandleFunc("/api/config", s.config)
	mux.HandleFunc("/api/message", s.message)
	mux.HandleFunc("/api/stop", s.stop)
	mux.HandleFunc("/api/approve", s.approve)
	mux.HandleFunc("/api/tools/", s.toggleTool)
	return mux
}

func (s *Server) snapshot() map[string]any {
	sessions := map[string]session.Snapshot{}
	if s.registry != nil {
		for _, item := range s.registry.List() {
			sessions[item.ID] = item.Snapshot(s.bus.Recent(item.ID))
		}
	}
	s.mu.RLock()
	masked := s.cfg.Masked()
	s.mu.RUnlock()
	return map[string]any{"sessions": sessions, "servers": masked.Servers, "config": masked, "serving_facts": servingFacts("SERVING.md"), "flow": map[string]any{"stages": events.Stages, "edges": [][2]string{{"assemble", "call_model"}, {"call_model", "parse"}, {"parse", "dispatch"}, {"dispatch", "execute"}, {"execute", "append"}, {"append", "assemble"}}}, "tools": []map[string]string{{"name": "read_file", "description": "Read a UTF-8 text file."}, {"name": "list_dir", "description": "List a directory."}, {"name": "write_file", "description": "Write a file."}, {"name": "edit_file", "description": "Edit a file."}, {"name": "grep", "description": "Search files."}, {"name": "shell", "description": "Run a shell command."}, {"name": "remember", "description": "Save a durable workspace note."}}}
}

func servingFacts(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{}
	}
	wanted := map[string]bool{"tokenize_idle_ms": true, "tokenize_busy_ms": true, "tokenize_blocks_on_slot": true}
	out := map[string]any{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && wanted[key] {
			out[key] = strings.TrimSpace(value)
		}
	}
	return out
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
func (s *Server) writeFrame(w http.ResponseWriter, event events.Event) {
	event.Body = nil
	event.Raw = nil
	data, _ := json.Marshal(event)
	fmt.Fprintf(w, "event: %s\ndata: %s\nid: %d\n\n", event.Type, data, event.Seq)
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	name := "index.html"
	if r.URL.Path == "/chat" {
		name = "chat.html"
	} else if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(s.webDir, name)
	if data, err := os.ReadFile(path); err == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, "<!doctype html><title>AgentB</title><p>AgentB API is running.</p>")
}
func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		values := []session.Snapshot{}
		for _, item := range s.registry.List() {
			values = append(values, item.Snapshot(s.bus.Recent(item.ID)))
		}
		writeJSON(w, 200, values)
	case http.MethodPost:
		var body struct {
			Label     string `json:"label"`
			ServerID  string `json:"server_id"`
			Workspace string `json:"workspace"`
		}
		if !decode(w, r, &body) {
			return
		}
		if body.Workspace == "" {
			s.mu.RLock()
			body.Workspace = s.cfg.Workspace
			s.mu.RUnlock()
		}
		if runnable, reason := s.registry.ProfileRunnable(body.ServerID); !runnable {
			writeError(w, 400, reason, "server_id")
			return
		}
		item, err := s.registry.Create(body.Label, body.ServerID, body.Workspace)
		if err != nil {
			writeError(w, 400, err.Error(), "session")
			return
		}
		if s.runner != nil {
			s.runner.PublishBudget(r.Context(), item)
		}
		writeJSON(w, 201, map[string]any{"session": item.Snapshot(nil)})
	default:
		method(w)
	}
}
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	parts := strings.Split(strings.Trim(tail, "/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "reset" && r.Method == http.MethodPost {
		if s.scheduler != nil && s.scheduler.Active(id) {
			if r.URL.Query().Get("force") != "1" {
				writeError(w, 409, "session is running", "session_id")
				return
			}
			s.scheduler.Stop(id, false)
			deadline := time.Now().Add(2 * time.Second)
			for s.scheduler.Active(id) && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
		}
		path, err := s.registry.Reset(id)
		if err != nil {
			writeError(w, 409, err.Error(), "session")
			return
		}
		if item, ok := s.registry.Get(id); ok && s.runner != nil {
			s.runner.PublishBudget(r.Context(), item)
		}
		writeJSON(w, 200, map[string]string{"log_path": path})
		return
	}
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Label string `json:"label"`
		}
		if !decode(w, r, &body) {
			return
		}
		if err := s.registry.Rename(id, body.Label); err != nil {
			writeError(w, 404, err.Error(), "session")
			return
		}
		writeJSON(w, 200, map[string]any{"session_id": id, "label": body.Label})
	case http.MethodDelete:
		err := s.registry.Close(id, r.URL.Query().Get("force") == "1")
		if err != nil {
			status := 404
			if strings.Contains(err.Error(), "running") {
				status = 409
			}
			writeError(w, status, err.Error(), "session")
			return
		}
		writeJSON(w, 200, map[string]string{"session_id": id})
	default:
		method(w)
	}
}
func (s *Server) servers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w)
		return
	}
	s.mu.RLock()
	masked := s.cfg.Masked()
	s.mu.RUnlock()
	writeJSON(w, 200, masked.Servers)
}
func (s *Server) server(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/servers/"), "/")
	if r.Method == http.MethodDelete && !strings.Contains(tail, "/") {
		if sessionID, used := s.registry.ProfileInUse(tail); used {
			writeError(w, 409, "profile in use by session "+sessionID, "server_id")
			return
		}
		s.mu.Lock()
		if len(s.cfg.Servers) == 1 {
			s.mu.Unlock()
			writeError(w, 409, "cannot delete the last profile", "server_id")
			return
		}
		found := false
		kept := s.cfg.Servers[:0]
		for _, profile := range s.cfg.Servers {
			if profile.ID == tail {
				found = true
				continue
			}
			kept = append(kept, profile)
		}
		s.cfg.Servers = kept
		if !found {
			s.mu.Unlock()
			writeError(w, 404, "server not found", "server_id")
			return
		}
		err := s.cfg.Save(s.configPath)
		masked := s.cfg.Masked()
		s.mu.Unlock()
		if err != nil {
			writeError(w, 500, err.Error(), "config")
			return
		}
		s.bus.Publish(events.New(events.ConfigChanged, "", "", map[string]any{"config": masked}))
		writeJSON(w, 200, map[string]string{"server_id": tail})
		return
	}
	if r.Method != http.MethodPost || !strings.HasSuffix(tail, "/probe") {
		method(w)
		return
	}
	id := strings.TrimSuffix(tail, "/probe")
	profile, ok := s.Profile(id)
	if !ok {
		writeError(w, 404, "server not found", "server_id")
		return
	}
	go s.runProbe(profile)
	writeJSON(w, 202, map[string]string{"status": "probing", "server_id": id})
}
func (s *Server) runProbe(profile *config.Profile) {
	caps, findings, err := probe.Probe(contextBackground{}, profile)
	if err != nil {
		caps = profile.Capabilities
		caps.ProbedAt = time.Now().UTC().Format(time.RFC3339)
		findings = []string{"probe failed: " + err.Error()}
		caps.Findings = findings
	}
	s.mu.Lock()
	for i := range s.cfg.Servers {
		if s.cfg.Servers[i].ID == profile.ID {
			s.cfg.Servers[i].Capabilities = caps
			s.cfg.Servers[i].Reasoning.ValidEfforts = append([]string(nil), caps.ValidEfforts...)
		}
	}
	saveErr := s.cfg.Save(s.configPath)
	s.mu.Unlock()
	if saveErr != nil {
		s.bus.Publish(events.New(events.Error, "", "", map[string]any{"where": "config", "message": saveErr.Error()}))
		return
	}
	if s.registry != nil {
		s.registry.RefreshRunnable()
	}
	s.bus.Publish(events.New(events.ServerProbed, "", "", map[string]any{"server_id": profile.ID, "capabilities": caps, "findings": findings}))
}

// contextBackground implements the small Context surface while avoiding probe cancellation after the request returns.
type contextBackground struct{}

func (contextBackground) Deadline() (time.Time, bool) { return time.Time{}, false }
func (contextBackground) Done() <-chan struct{}       { return nil }
func (contextBackground) Err() error                  { return nil }
func (contextBackground) Value(any) any               { return nil }

func (s *Server) config(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		masked := s.cfg.Masked()
		s.mu.RUnlock()
		writeJSON(w, 200, masked)
	case http.MethodPost:
		var patch map[string]any
		if !decode(w, r, &patch) {
			return
		}
		s.mu.Lock()
		currentBytes, _ := json.Marshal(s.cfg)
		var current map[string]any
		_ = json.Unmarshal(currentBytes, &current)
		mergeConfig(current, patch)
		merged, _ := json.Marshal(current)
		var next config.Config
		if err := json.Unmarshal(merged, &next); err != nil {
			s.mu.Unlock()
			writeError(w, 400, err.Error(), "config")
			return
		}
		config.ApplyDefaults(&next)
		if err := next.Validate(); err != nil {
			s.mu.Unlock()
			writeError(w, 400, err.Error(), configField(err, next))
			return
		}
		if err := next.Save(s.configPath); err != nil {
			s.mu.Unlock()
			writeError(w, 500, err.Error(), "config")
			return
		}
		s.cfg = &next
		masked := next.Masked()
		s.mu.Unlock()
		if s.registry != nil {
			s.registry.RefreshRunnable()
		}
		if s.prompt != nil {
			if err := s.prompt.Reload(); err != nil {
				writeError(w, 500, err.Error(), "prompts/system.md")
				return
			}
		}
		if s.runner != nil {
			go func() {
				for _, item := range s.registry.List() {
					s.runner.PublishBudget(contextBackground{}, item)
				}
			}()
		}
		s.bus.Publish(events.New(events.ConfigChanged, "", "", map[string]any{"config": masked}))
		writeJSON(w, 200, masked)
	default:
		method(w)
	}
}

func configField(err error, cfg config.Config) string {
	field := strings.SplitN(err.Error(), ":", 2)[0]
	if !strings.HasPrefix(field, "servers[") {
		return field
	}
	end := strings.Index(field, "]")
	if end < 9 {
		return field
	}
	var index int
	if _, scanErr := fmt.Sscanf(field[:end+1], "servers[%d]", &index); scanErr != nil || index < 0 || index >= len(cfg.Servers) {
		return field
	}
	return "servers." + cfg.Servers[index].ID + field[end+1:]
}
func (s *Server) message(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		method(w)
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
		Text      string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	result, err := s.scheduler.Submit(r.Context(), body.SessionID, body.Text)
	if err != nil {
		status := 400
		if strings.Contains(err.Error(), "in progress") || strings.Contains(err.Error(), "queue full") {
			status = 409
		}
		writeError(w, status, err.Error(), "session_id")
		return
	}
	writeJSON(w, 202, result)
}
func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		method(w)
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
		All       bool   `json:"all"`
	}
	if !decode(w, r, &body) {
		return
	}
	writeJSON(w, 200, map[string]any{"stopped": s.scheduler.Stop(body.SessionID, body.All)})
}
func (s *Server) approve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		method(w)
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
		CallID    string `json:"call_id"`
		Decision  string `json:"decision"`
	}
	if !decode(w, r, &body) {
		return
	}
	if s.runner == nil {
		writeError(w, 409, "runtime unavailable", "call_id")
		return
	}
	if err := s.runner.Gate().Decide(body.SessionID, body.CallID, body.Decision); err != nil {
		status := 400
		if strings.Contains(err.Error(), "not found") {
			status = 404
		}
		writeError(w, status, err.Error(), "call_id")
		return
	}
	writeJSON(w, 200, map[string]any{"session_id": body.SessionID, "call_id": body.CallID, "decision": body.Decision})
}
func (s *Server) toggleTool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		method(w)
		return
	}
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/tools/"), "/")
	var body struct {
		SessionID string `json:"session_id"`
		Enabled   bool   `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	item, ok := s.registry.Get(body.SessionID)
	if !ok {
		writeError(w, 404, "session not found", "session_id")
		return
	}
	if !item.ToggleTool(name, body.Enabled) {
		writeError(w, 404, "tool not found", "name")
		return
	}
	s.bus.Publish(events.New(events.ToolToggled, item.ID, "", map[string]any{"name": name, "enabled": body.Enabled}))
	s.runner.PublishBudget(r.Context(), item)
	writeJSON(w, 200, map[string]any{"name": name, "enabled": body.Enabled})
}
func mergeConfig(dst, src map[string]any) {
	for key, value := range src {
		if key == "servers" {
			incoming, _ := value.([]any)
			existing, _ := dst[key].([]any)
			byID := map[string]map[string]any{}
			order := []string{}
			for _, raw := range existing {
				item, _ := raw.(map[string]any)
				id, _ := item["id"].(string)
				byID[id] = item
				order = append(order, id)
			}
			for _, raw := range incoming {
				item, _ := raw.(map[string]any)
				id, _ := item["id"].(string)
				if id == "" {
					continue
				}
				if item["api_key"] == "•••• set" {
					delete(item, "api_key")
				}
				if byID[id] == nil {
					byID[id] = map[string]any{"id": id}
					order = append(order, id)
				}
				mergeConfig(byID[id], item)
			}
			out := make([]any, 0, len(order))
			for _, id := range order {
				out = append(out, byID[id])
			}
			dst[key] = out
			continue
		}
		child, childOK := value.(map[string]any)
		target, targetOK := dst[key].(map[string]any)
		if childOK && targetOK {
			mergeConfig(target, child)
		} else {
			dst[key] = value
		}
	}
}
func notBuilt(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 501, map[string]string{"error": "not built yet (prompt 4)"})
}
func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(w, 400, err.Error(), "body")
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, message, field string) {
	writeJSON(w, status, map[string]string{"error": message, "field": field})
}
func method(w http.ResponseWriter) { writeError(w, 405, "method not allowed", "method") }
