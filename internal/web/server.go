package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"harness/internal/agent"
	"harness/internal/config"
	"harness/internal/credential"
	"harness/internal/events"
	"harness/internal/hardening"
	"harness/internal/probe"
	"harness/internal/serviceaccount"
	"harness/internal/session"
	"harness/internal/tools"
)

type Server struct {
	mu               sync.RWMutex
	cfg              *config.Config
	configPath       string
	roots            RuntimeRoots
	bus              *events.Bus
	registry         *session.Registry
	webDir           string
	scheduler        *agent.Scheduler
	runner           *agent.Runner
	prompt           *agent.PromptRenderer
	replay           *events.Replay
	credential       *credential.Store
	shell            *tools.Shell
	account          serviceaccount.Manager
	hardening        hardening.Manager
	hardeningMu      sync.RWMutex
	hardeningOp      hardeningOperation
	shellTest        func(context.Context) (string, error)
	accountMu        sync.Mutex
	mutationToken    string
	operatorChangeMu sync.Mutex
	operatorMu       sync.Mutex
	operatorEnabled  bool
	operatorExpires  string
	operatorTimer    operatorTimer
	operatorEpoch    uint64
	operatorRequest  func(*http.Request) error
	operatorNow      func() time.Time
	operatorAfter    func(time.Duration, func()) operatorTimer
}

type RuntimeRoots struct {
	Application string
	Data        string
	Workspace   string
}

type operatorTimer interface {
	Stop() bool
}

func New(cfg *config.Config, path, webDir string, roots RuntimeRoots, bus *events.Bus) *Server {
	cfg.Shell.OperatorContext = false
	cfg.Shell.OperatorContextExpiresAt = ""
	return &Server{
		cfg: cfg, configPath: path, webDir: webDir, roots: roots, bus: bus, mutationToken: newMutationToken(),
		operatorRequest: requireOperatorHTTPClient,
		operatorNow:     time.Now,
		operatorAfter: func(duration time.Duration, fn func()) operatorTimer {
			return time.AfterFunc(duration, fn)
		},
	}
}
func (s *Server) SetRegistry(registry *session.Registry) { s.registry = registry }
func (s *Server) SetReplay(replay *events.Replay)        { s.replay = replay }
func (s *Server) SetShellSecurity(store *credential.Store, shell *tools.Shell) {
	s.credential = store
	s.shell = shell
	s.shellTest = shell.TestServiceAccount
}
func (s *Server) SetServiceAccountManager(manager serviceaccount.Manager) { s.account = manager }
func (s *Server) SetHardeningManager(manager hardening.Manager)           { s.hardening = manager }
func (s *Server) SetRuntime(scheduler *agent.Scheduler, runner *agent.Runner, prompt *agent.PromptRenderer) {
	s.scheduler = scheduler
	s.runner = runner
	s.prompt = prompt
	if runner != nil {
		runner.SetToolActivity(func(phase string) {
			s.touchOperatorContext("idle window reset: tool execution " + phase)
		})
	}
}
func (s *Server) ConfigSnapshot() config.Config {
	s.mu.RLock()
	result := *s.cfg
	s.mu.RUnlock()
	s.operatorMu.Lock()
	result.Shell.OperatorContext = s.operatorEnabled
	result.Shell.OperatorContextExpiresAt = s.operatorExpires
	s.operatorMu.Unlock()
	return result
}
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
	mux.HandleFunc("/api/sessions", s.replayGuard(s.sessions))
	mux.HandleFunc("/api/sessions/", s.replayGuard(s.session))
	mux.HandleFunc("/api/servers", s.servers)
	mux.HandleFunc("/api/servers/", s.replayGuard(s.server))
	mux.HandleFunc("/api/config", s.replayGuard(s.config))
	mux.HandleFunc("/api/shell-credential", s.replayGuard(s.shellCredential))
	mux.HandleFunc("/api/service-account", s.replayGuard(s.serviceAccount))
	mux.HandleFunc("/api/hardening", s.replayGuard(s.hostHardening))
	mux.HandleFunc("/api/message", s.replayGuard(s.message))
	mux.HandleFunc("/api/stop", s.replayGuard(s.stop))
	mux.HandleFunc("/api/approve", s.replayGuard(s.approve))
	mux.HandleFunc("/api/tools/", s.replayGuard(s.toggleTool))
	return s.securityHeaders(s.mutationGuard(mux))
}

func (s *Server) hardeningRequest(serverID string) (hardening.Request, error) {
	s.mu.RLock()
	cfg := *s.cfg
	s.mu.RUnlock()
	if serverID == "" && len(cfg.Servers) > 0 {
		serverID = cfg.Servers[0].ID
	}
	var profile *config.Profile
	for index := range cfg.Servers {
		if cfg.Servers[index].ID == serverID {
			value := cfg.Servers[index]
			profile = &value
			break
		}
	}
	if profile == nil {
		return hardening.Request{}, fmt.Errorf("model profile not found")
	}
	endpoint, err := url.Parse(profile.BaseURL)
	if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return hardening.Request{}, fmt.Errorf("model profile base_url is invalid")
	}
	host := endpoint.Hostname()
	if strings.EqualFold(host, "localhost") {
		host = "127.0.0.1"
	}
	if net.ParseIP(host) == nil {
		return hardening.Request{}, fmt.Errorf("hardening requires a numeric loopback or Tailscale model address; profile host %q is not numeric", host)
	}
	port := 0
	if endpoint.Port() != "" {
		if _, err := fmt.Sscanf(endpoint.Port(), "%d", &port); err != nil {
			return hardening.Request{}, fmt.Errorf("model profile port is invalid")
		}
	} else if endpoint.Scheme == "https" {
		port = 443
	} else {
		port = 80
	}
	if port < 1 || port > 65535 {
		return hardening.Request{}, fmt.Errorf("model profile port must be between 1 and 65535")
	}
	return hardening.Request{
		AccountName: cfg.Shell.ServiceAccount.Account, ApplicationDirectory: s.roots.Application,
		DataDirectory: s.roots.Data, WorkspaceDirectory: s.roots.Workspace,
		ModelAddress: host, ModelPort: port,
	}, nil
}

func newMutationToken() string {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		panic("generate HTTP mutation token: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(value)
}

func (s *Server) mutationGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		provided := r.Header.Get("X-AgentB-Mutation-Token")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(s.mutationToken)) != 1 {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing or invalid Agent_b mutation token"})
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !sameRequestOrigin(origin, r.Host) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin mutation refused"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameRequestOrigin(origin, requestHost string) bool {
	parsed, err := url.Parse(origin)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && strings.EqualFold(parsed.Host, requestHost)
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) replayGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.replay != nil && r.Method != http.MethodGet {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "replay mode"})
			return
		}
		next(w, r)
	}
}

func (s *Server) snapshot() map[string]any {
	if s.replay != nil {
		return s.snapshotWithSessions(s.replay.Sessions, true)
	}
	sessions := map[string]session.Snapshot{}
	for _, item := range s.registry.List() {
		sessions[item.ID] = item.Snapshot(s.bus.Recent(item.ID))
	}
	return s.snapshotWithSessions(sessions, false)
}
func (s *Server) snapshotWithSessions(sessions any, replay bool) map[string]any {
	masked := s.ConfigSnapshot().Masked()
	credentialStatus := credential.Status{}
	identityStatus := tools.ShellIdentityStatus{}
	if s.credential != nil {
		credentialStatus = s.credential.Status()
	}
	if s.shell != nil {
		identityStatus = s.shell.IdentityStatus()
	}
	return map[string]any{
		"sessions": sessions, "servers": masked.Servers, "config": masked, "replay": replay,
		"mutation_token": s.mutationToken, "shell_credential": credentialStatus, "shell_identity": identityStatus,
		"serving_facts": servingFacts(filepath.Join(s.roots.Application, "SERVING.md")),
		"flow":          map[string]any{"stages": events.Stages, "edges": [][2]string{{"assemble", "call_model"}, {"call_model", "parse"}, {"parse", "dispatch"}, {"dispatch", "execute"}, {"execute", "append"}, {"append", "assemble"}}},
		"tools": []map[string]string{
			{"name": "read_file", "description": "Read numbered local UTF-8 text by byte offset and limit. When more is true, pass returned next_offset as offset to advance. Unlike fetch_url, it reads the filesystem."},
			{"name": "list_dir", "description": "List entries under local directory path to depth. Unlike find_files, it enumerates contents without a filename pattern."},
			{"name": "write_file", "description": "Create or fully replace the file at path with content, creating parent directories. Unlike edit_file, it writes the whole file."},
			{"name": "edit_file", "description": "Replace one exact, unique old_string in path with new_string. Unlike write_file, it avoids reproducing the whole file."},
			{"name": "search_text", "description": "Search local file contents under path for pattern, optionally filtering filenames with glob. Unlike find_files, it returns matching text lines."},
			{"name": "shell", "description": "Run an unconfined inline command from the workspace root. Shell has no network in service context (enforced outside the tool layer); use fetch_url for every network operation. Script artifacts written by an agent cannot be executed."},
			{"name": "remember", "description": "Save note as durable workspace memory for future sessions. Call recall first to avoid duplicates; unlike recall, remember writes."},
			{"name": "recall", "description": "Read all durable workspace notes; takes no arguments. Use before remember to avoid duplicates; unlike recall, remember never writes."},
			{"name": "fetch_url", "description": "Fetch untrusted public HTTP(S) text by byte offset and limit. When more is true, pass returned next_offset as offset to advance. Unlike read_file, it uses the network."},
			{"name": "find_files", "description": "Find local files under path whose names or relative paths match pattern. Unlike search_text, it does not inspect file contents."},
		},
	}
}

func (s *Server) shellCredential(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		method(w)
		return
	}
	if !s.accountMu.TryLock() {
		writeError(w, http.StatusConflict, "service-account setup or credential update is already in progress", "shell.service_account")
		return
	}
	defer s.accountMu.Unlock()
	if s.credential == nil || s.shell == nil {
		writeError(w, http.StatusConflict, "shell credential runtime is unavailable", "shell.service_account")
		return
	}
	var body struct {
		Action   string `json:"action"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	switch body.Action {
	case "store":
		if body.Password == "" {
			writeError(w, http.StatusBadRequest, "password is required", "shell.service_account.password")
			return
		}
		password := []byte(body.Password)
		body.Password = ""
		err := s.credential.Write(password)
		for index := range password {
			password[index] = 0
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "shell.service_account.password")
			return
		}
		status := s.credential.Status()
		s.bus.Publish(events.New(events.ShellCredential, "", "", status))
		writeJSON(w, http.StatusOK, status)
	case "clear":
		if err := s.credential.Clear(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "shell.service_account.password")
			return
		}
		status := s.credential.Status()
		s.bus.Publish(events.New(events.ShellCredential, "", "", status))
		writeJSON(w, http.StatusOK, status)
	case "test":
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		message, err := s.shell.TestServiceAccount(ctx)
		if err != nil {
			writeError(w, http.StatusBadRequest, message, "shell.service_account")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": message, "credential": s.credential.Status(), "identity": s.shell.IdentityStatus()})
	default:
		writeError(w, http.StatusBadRequest, "action must be store, test, or clear", "action")
	}
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
	if s.replay != nil {
		s.replaySSE(w, r, flusher)
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
	for _, recorded := range s.replay.Events {
		if !instant {
			timer := time.NewTimer(20 * time.Millisecond)
			select {
			case <-timer.C:
			case <-r.Context().Done():
				timer.Stop()
				return
			}
		}
		s.writeFrame(w, recorded)
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
	fmt.Fprint(w, "<!doctype html><title>Agent_b</title><p>Agent_b API is running.</p>")
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
			Label    *string `json:"label"`
			ServerID *string `json:"server_id"`
		}
		if !decode(w, r, &body) {
			return
		}
		if body.Label == nil && body.ServerID == nil {
			writeError(w, 400, "label or server_id is required", "session")
			return
		}
		if body.ServerID != nil {
			if err := s.registry.SetServer(id, *body.ServerID); err != nil {
				status := http.StatusBadRequest
				field := "server_id"
				if strings.Contains(err.Error(), "not found") {
					status, field = http.StatusNotFound, "session"
				} else if strings.Contains(err.Error(), "running") {
					status, field = http.StatusConflict, "session"
				}
				writeError(w, status, err.Error(), field)
				return
			}
		}
		if body.Label != nil {
			if err := s.registry.Rename(id, *body.Label); err != nil {
				writeError(w, 404, err.Error(), "session")
				return
			}
		}
		item, ok := s.registry.Get(id)
		if !ok {
			writeError(w, 404, "session not found", "session")
			return
		}
		if body.ServerID != nil && s.runner != nil {
			s.runner.PublishBudget(r.Context(), item)
		}
		writeJSON(w, 200, map[string]any{"session": item.Snapshot(nil)})
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
	if reason := config.ProfileSetupReason(profile); reason != "" {
		writeError(w, 400, reason, "servers."+id)
		return
	}
	go s.runProbe(profile)
	writeJSON(w, 202, map[string]string{"status": "probing", "server_id": id})
}
func (s *Server) runProbe(profile *config.Profile) {
	caps, findings, err := probe.Probe(contextBackground{}, profile)
	if err != nil {
		caps, findings = failedProbeCapabilities(profile, err)
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

func failedProbeCapabilities(profile *config.Profile, err error) (config.Capabilities, []string) {
	caps := profile.Capabilities
	findings := []string{"probe failed: " + err.Error()}
	caps.Findings = findings
	return caps, findings
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
		writeJSON(w, 200, s.ConfigSnapshot().Masked())
	case http.MethodPost:
		var patch map[string]any
		if !decode(w, r, &patch) {
			return
		}
		if !s.requireOperatorConfigRequest(w, r, patch) {
			return
		}
		if s.applyOperatorContextPatch(w, r, patch) {
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
		workspaceRoot, err := filepath.Abs(next.Workspace)
		if err != nil {
			s.mu.Unlock()
			writeError(w, 400, err.Error(), "workspace")
			return
		}
		if err := next.Save(s.configPath); err != nil {
			s.mu.Unlock()
			writeError(w, 500, err.Error(), "config")
			return
		}
		s.cfg = &next
		s.roots.Workspace = filepath.Clean(workspaceRoot)
		masked := next.Masked()
		s.mu.Unlock()
		if s.runner != nil {
			s.runner.Configure(s.ConfigSnapshot())
		}
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
