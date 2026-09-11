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
	"harness/internal/buildinfo"
	"harness/internal/config"
	"harness/internal/credential"
	"harness/internal/detection"
	"harness/internal/events"
	"harness/internal/hardening"
	"harness/internal/memory"
	"harness/internal/operatorfiles"
	"harness/internal/probe"
	"harness/internal/projection"
	"harness/internal/serviceaccount"
	"harness/internal/session"
	"harness/internal/signing"
	"harness/internal/stats"
	"harness/internal/tools"
	workspaceinfo "harness/internal/workspace"
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
	replay           *projection.Replay
	projector        *projection.Store
	writers          *events.Writers
	credential       *credential.Store
	shell            *tools.Shell
	account          serviceaccount.Manager
	hardening        hardening.Manager
	hardeningMu      sync.RWMutex
	hardeningOp      hardeningOperation
	signing          signing.Manager
	signingMu        sync.Mutex
	signingStatus    signing.Status
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
	openFolder       func(string) error
	extractClient    *http.Client
	detectLocal      func(context.Context, string) (any, error)
	workspaceState   *workspaceinfo.Manager
	memoryState      *memory.Manager
	statsState       *stats.Manager
	operatorFiles    *operatorfiles.Manager
	pickFolder       func(string) (string, error)
	probeMu          sync.Mutex
	probeCancels     map[string]*probeRun
	bindMu           sync.Mutex
	pendingBinds     map[string]pendingBind
	navigationMu     sync.Mutex
	navigationIDs    map[string]time.Time
}

type probeRun struct{ cancel context.CancelFunc }
type pendingBind struct {
	Dir         string
	Text        string
	Attachments []events.Attachment
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
		openFolder:    openContainingFolder,
		probeCancels:  map[string]*probeRun{},
		pendingBinds:  map[string]pendingBind{},
		navigationIDs: map[string]time.Time{},
		extractClient: &http.Client{},
		detectLocal: func(ctx context.Context, account string) (any, error) {
			return detection.Local(ctx, filepath.Join(roots.Application, "scripts", "detect-local-capabilities.ps1"), account)
		},
		pickFolder: nativeFolderPicker,
	}
}
func (s *Server) SetRegistry(registry *session.Registry) { s.registry = registry }
func (s *Server) SetWorkspaceState(manager *workspaceinfo.Manager, memories *memory.Manager) {
	s.workspaceState, s.memoryState = manager, memories
}
func (s *Server) SetStats(manager *stats.Manager)                 { s.statsState = manager }
func (s *Server) SetOperatorFiles(manager *operatorfiles.Manager) { s.operatorFiles = manager }
func (s *Server) SetReplay(replay *projection.Replay)             { s.replay = replay }
func (s *Server) SetProjection(projector *projection.Store, writers *events.Writers) {
	s.projector, s.writers = projector, writers
}
func (s *Server) SetShellSecurity(store *credential.Store, shell *tools.Shell) {
	s.credential = store
	s.shell = shell
	s.shellTest = shell.TestServiceAccount
}
func (s *Server) SetServiceAccountManager(manager serviceaccount.Manager) { s.account = manager }
func (s *Server) SetHardeningManager(manager hardening.Manager)           { s.hardening = manager }
func (s *Server) SetSigningManager(manager signing.Manager)               { s.signing = manager }
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
	return s.cfg.Profile(id)
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.page)
	mux.HandleFunc("/chat", s.page)
	mux.HandleFunc("/plan", s.page)
	mux.HandleFunc("/setup", s.page)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(s.webDir))))
	mux.HandleFunc("/api/events", s.sse)
	mux.HandleFunc("/api/state", s.state)
	mux.HandleFunc("/api/local-detection", s.localDetection)
	mux.HandleFunc("/api/files/", s.file)
	mux.HandleFunc("/api/open-folder", s.openFileFolder)
	mux.HandleFunc("/api/attachments", s.replayGuard(s.attachments))
	mux.HandleFunc("/api/exchange-files", s.exchangeFiles)
	mux.HandleFunc("/api/operator-attachments", s.operatorAttachments)
	mux.HandleFunc("/api/operator-files", s.replayGuard(s.operatorFileState))
	mux.HandleFunc("/api/ui-errors", s.replayGuard(s.uiError))
	mux.HandleFunc("/api/navigation-starts", s.replayGuard(s.navigationStart))
	mux.HandleFunc("/api/navigation-measurements", s.replayGuard(s.navigationMeasurement))
	mux.HandleFunc("/api/navigation-suppressions", s.replayGuard(s.navigationSuppression))
	mux.HandleFunc("/api/sessions", s.replayGuard(s.sessions))
	mux.HandleFunc("/api/sessions/", s.replayGuard(s.session))
	mux.HandleFunc("/api/workspaces", s.replayGuard(s.workspaces))
	mux.HandleFunc("/api/workspaces/", s.replayGuard(s.workspaceAction))
	mux.HandleFunc("/api/pick-folder", s.replayGuard(s.folderPicker))
	mux.HandleFunc("/api/servers", s.servers)
	mux.HandleFunc("/api/servers/", s.replayGuard(s.server))
	mux.HandleFunc("/api/config", s.replayGuard(s.config))
	mux.HandleFunc("/api/shell-credential", s.replayGuard(s.shellCredential))
	mux.HandleFunc("/api/service-account", s.replayGuard(s.serviceAccount))
	mux.HandleFunc("/api/hardening", s.replayGuard(s.hostHardening))
	mux.HandleFunc("/api/signing", s.replayGuard(s.codeSigning))
	mux.HandleFunc("/api/message", s.replayGuard(s.message))
	mux.HandleFunc("/api/bind", s.replayGuard(s.bindWorkspace))
	mux.HandleFunc("/api/stop", s.replayGuard(s.stop))
	mux.HandleFunc("/api/approve", s.replayGuard(s.approve))
	mux.HandleFunc("/api/tools/", s.replayGuard(s.toggleTool))
	mux.HandleFunc("/api/stats/", s.replayGuard(s.stats))
	mux.HandleFunc("/api/agents/", s.replayGuard(s.agentAction))
	return s.securityHeaders(s.mutationGuard(mux))
}

func (s *Server) hardeningRequest(serverID string) (hardening.Request, error) {
	s.mu.RLock()
	cfg := *s.cfg
	s.mu.RUnlock()
	if serverID == "" {
		if agent, ok := cfg.Agent(cfg.DefaultAgentID()); ok {
			serverID = agent.B
		}
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
	exchange, err := cfg.ResolvedExchangeFolder()
	if err != nil {
		return hardening.Request{}, err
	}
	return hardening.Request{
		AccountName: cfg.Shell.ServiceAccount.Account, ApplicationDirectory: s.roots.Application,
		DataDirectory: s.roots.Data, WorkspaceDirectory: s.roots.Workspace, ExchangeDirectory: exchange,
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
	if s.credential != nil {
		credentialStatus = s.credential.Status()
	}
	if s.shell != nil {
		identityStatus = s.shell.IdentityStatus()
	}
	return map[string]any{
		"sessions": sessions, "servers": masked.Servers, "config": masked, "replay": replay,
		"build":          buildinfo.Current(),
		"signature":      s.signingState(),
		"mutation_token": s.mutationToken, "shell_credential": credentialStatus, "shell_identity": identityStatus,
		"serving_facts": servingFacts(filepath.Join(s.roots.Application, "SERVING.md")),
		"flow":          map[string]any{"stages": events.Stages, "edges": [][2]string{{"assemble", "call_model"}, {"call_model", "parse"}, {"parse", "dispatch"}, {"dispatch", "execute"}, {"execute", "append"}, {"append", "assemble"}}},
		"tools": []map[string]string{
			{"name": "read_file", "description": "Read numbered local UTF-8 text by byte offset and limit. When more is true, pass returned next_offset as offset to advance. Unlike fetch_url, it reads the filesystem."},
			{"name": "list_dir", "description": "List entries under local directory path to depth. Unlike find_files, it enumerates contents without a filename pattern."},
			{"name": "write_file", "description": "Create or fully replace the file at path with content, creating parent directories. Unlike edit_file, it writes the whole file."},
			{"name": "edit_file", "description": "Replace one exact, unique old_string in path with new_string. Unlike write_file, it avoids reproducing the whole file."},
			{"name": "search_text", "description": "Search local file contents under path for pattern, optionally filtering filenames with glob. Unlike find_files, it returns matching text lines."},
			{"name": "shell", "description": shellDescription},
			{"name": "remember", "description": "Save note as durable workspace memory for future sessions. Call recall first to avoid duplicates; unlike recall, remember writes."},
			{"name": "recall", "description": "Read all durable workspace notes; takes no arguments. Use before remember to avoid duplicates; unlike recall, remember never writes."},
			{"name": "fetch_url", "description": "Fetch untrusted public HTTP(S) text by byte offset and limit. When more is true, pass returned next_offset as offset to advance. Unlike read_file, it uses the network."},
			{"name": "find_files", "description": "Find local files under path whose names or relative paths match pattern. Unlike search_text, it does not inspect file contents."},
			{"name": "run_script", "description": tools.NewRunScript(tools.NewShell(masked.Shell)).Description()},
			{"name": "call_service", "description": tools.NewCallService(masked.Services).Description()},
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
	name := "index.html"
	if r.URL.Path == "/setup" {
		name = "setup.html"
	} else if r.URL.Path == "/chat" {
		name = "chat.html"
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
func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		values := []projection.Snapshot{}
		if s.projector != nil && s.writers != nil {
			projected, err := s.projector.Snapshot(s.writers.SessionCursors())
			if err != nil {
				writeError(w, 500, err.Error(), "projection")
				return
			}
			for _, value := range projected {
				values = append(values, value)
			}
		}
		writeJSON(w, 200, values)
	case http.MethodPost:
		var body struct {
			Label           string `json:"label"`
			AgentID         string `json:"agent_id"`
			ServerID        string `json:"server_id"`
			Workspace       string `json:"workspace"`
			SourceSessionID string `json:"source_session_id"`
		}
		if !decode(w, r, &body) {
			return
		}
		if body.SourceSessionID != "" {
			if body.Label != "" || body.AgentID != "" || body.ServerID != "" {
				writeError(w, 400, "source_session_id cannot be combined with overrides", "session")
				return
			}
			item, err := s.registry.CreateLikeAt(body.SourceSessionID, body.Workspace)
			if err != nil {
				writeError(w, 400, err.Error(), "session")
				return
			}
			if s.runner != nil {
				s.runner.PublishBudget(r.Context(), item)
			}
			writeJSON(w, 201, map[string]any{"session": item.Snapshot()})
			return
		}
		if body.AgentID == "" && body.ServerID != "" {
			s.mu.RLock()
			for _, candidate := range s.cfg.Agents {
				if candidate.B == body.ServerID {
					body.AgentID = config.AgentID(candidate.Name)
					break
				}
			}
			s.mu.RUnlock()
		}
		if body.Workspace == "" {
			s.mu.RLock()
			body.Workspace = s.cfg.Workspace
			if body.AgentID == "" {
				body.AgentID = s.cfg.DefaultAgentID()
			}
			s.mu.RUnlock()
		} else if body.AgentID == "" {
			s.mu.RLock()
			body.AgentID = s.cfg.DefaultAgentID()
			s.mu.RUnlock()
		}
		if runnable, reason := s.registry.AgentRunnable(body.AgentID); !runnable {
			writeError(w, 400, reason, "agent_id")
			return
		}
		item, err := s.registry.Create(body.Label, body.AgentID, body.Workspace)
		if err != nil {
			writeError(w, 400, err.Error(), "session")
			return
		}
		if s.runner != nil {
			s.runner.PublishBudget(r.Context(), item)
		}
		writeJSON(w, 201, map[string]any{"session": item.Snapshot()})
	default:
		method(w)
	}
}

func (s *Server) workspaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w)
		return
	}
	if s.workspaceState == nil {
		writeJSON(w, http.StatusOK, []workspaceinfo.Entry{})
		return
	}
	writeJSON(w, http.StatusOK, s.workspaceState.List())
}

func (s *Server) workspaceAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || s.workspaceState == nil {
		method(w)
		return
	}
	action := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/workspaces/"), "/")
	var body struct {
		Dir       string `json:"dir"`
		Hash      string `json:"hash"`
		SessionID string `json:"session_id"`
		Confirm   bool   `json:"confirm"`
	}
	if !decode(w, r, &body) {
		return
	}
	switch action {
	case "memory-clear":
		if !body.Confirm {
			writeError(w, http.StatusBadRequest, "memory clear requires confirmation", "confirm")
			return
		}
		if s.memoryState == nil {
			writeError(w, 500, "memory manager unavailable", "memory")
			return
		}
		if err := s.memoryState.Clear(body.Dir); err != nil {
			writeError(w, 500, err.Error(), "memory")
			return
		}
		s.registry.ClearWorkspaceMemory(body.Dir)
		for _, item := range s.registry.List() {
			if strings.EqualFold(filepath.Clean(item.Workspace), filepath.Clean(body.Dir)) {
				s.bus.Publish(events.New(events.MemoryCleared, item.ID, "", map[string]any{"dir": body.Dir}))
			}
		}
		writeJSON(w, 200, map[string]any{"dir": body.Dir, "cleared": true})
	case "policy-approve":
		state, err := s.workspaceState.Approve(body.Dir, body.Hash)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error(), "policy")
			return
		}
		if err := s.registry.ApplyRepoPolicySession(body.SessionID, state); err != nil {
			writeError(w, http.StatusNotFound, err.Error(), "session")
			return
		}
		snapshot, _ := s.registry.Get(body.SessionID)
		s.bus.Publish(events.New(events.PolicyApproved, body.SessionID, "", map[string]any{"dir": body.Dir, "path": state.Path, "hash": state.Hash, "approved_at": state.ApprovedAt, "approved": true, "persisted": true, "scope": "session", "tools": snapshot.Snapshot().Tools}))
		writeJSON(w, 200, state)
	case "policy-once":
		state, err := s.workspaceState.PolicyForDecision(body.Dir, body.Hash)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error(), "policy")
			return
		}
		state.Approved = true
		if err := s.registry.ApplyRepoPolicySession(body.SessionID, state); err != nil {
			writeError(w, http.StatusNotFound, err.Error(), "session")
			return
		}
		snapshot, _ := s.registry.Get(body.SessionID)
		s.bus.Publish(events.New(events.PolicyApproved, body.SessionID, "", map[string]any{"dir": body.Dir, "path": state.Path, "hash": state.Hash, "approved": true, "persisted": false, "scope": "session", "tools": snapshot.Snapshot().Tools}))
		writeJSON(w, 200, state)
	case "policy-deny":
		if err := s.registry.DenyRepoPolicy(body.SessionID); err != nil {
			writeError(w, 404, err.Error(), "session")
			return
		}
		s.bus.Publish(events.New(events.PolicyDenied, body.SessionID, "", map[string]any{"dir": body.Dir, "hash": body.Hash}))
		writeJSON(w, 200, map[string]any{"denied": true})
	case "policy-revoke":
		if err := s.workspaceState.Revoke(body.Dir); err != nil {
			writeError(w, 500, err.Error(), "policy")
			return
		}
		s.registry.RevokeRepoPolicy(body.Dir)
		for _, item := range s.registry.List() {
			if strings.EqualFold(filepath.Clean(item.Workspace), filepath.Clean(body.Dir)) {
				s.bus.Publish(events.New(events.PolicyRevoked, item.ID, "", map[string]any{"dir": body.Dir}))
			}
		}
		writeJSON(w, 200, map[string]any{"dir": body.Dir, "revoked": true})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) folderPicker(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		recent := []workspaceinfo.Entry{}
		if s.workspaceState != nil {
			recent = s.workspaceState.List()
		}
		writeJSON(w, 200, map[string]any{"default": s.roots.Workspace, "recent": recent})
		return
	}
	if r.Method != http.MethodPost {
		method(w)
		return
	}
	if err := s.operatorRequest(r); err != nil {
		writeError(w, http.StatusForbidden, "folder selection requires a verified operator browser process: "+err.Error(), "workspace")
		return
	}
	var body struct {
		Default string `json:"default"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Default == "" {
		body.Default = s.roots.Workspace
	}
	selected, err := s.pickFolder(body.Default)
	if err != nil {
		writeError(w, 400, err.Error(), "workspace")
		return
	}
	writeJSON(w, 200, map[string]string{"workspace_dir": selected})
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	parts := strings.Split(strings.Trim(tail, "/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 3 && parts[1] == "grants" && parts[2] == "revoke" && r.Method == http.MethodPost {
		if err := s.operatorRequest(r); err != nil {
			writeError(w, http.StatusForbidden, "Run as you can be revoked only by the verified local operator process", "session_id")
			return
		}
		if _, ok := s.registry.Get(id); !ok {
			writeError(w, http.StatusNotFound, "session not found", "session_id")
			return
		}
		if s.runner != nil {
			s.runner.RevokeSessionGrants(id)
		}
		writeJSON(w, http.StatusOK, map[string]any{"session_id": id, "revoked": true})
		return
	}
	if len(parts) == 3 && parts[1] == "messages" && parts[2] == "drop-last" && r.Method == http.MethodPost {
		message, err := s.registry.DropLastMessage(id)
		if err != nil {
			status := http.StatusConflict
			if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error(), "session")
			return
		}
		if item, ok := s.registry.Get(id); ok && s.runner != nil {
			s.runner.PublishBudget(r.Context(), item)
		}
		writeJSON(w, http.StatusOK, map[string]any{"message_id": message.ID, "role": message.Role})
		return
	}
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
	if len(parts) == 2 && parts[1] == "delete" && r.Method == http.MethodPost {
		if err := s.operatorRequest(r); err != nil {
			writeError(w, http.StatusForbidden, "full chat deletion requires a verified local operator process", "session_id")
			return
		}
		item, ok := s.registry.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "session not found", "session_id")
			return
		}
		if !item.IsClosed() {
			writeError(w, http.StatusConflict, "session must be closed before deletion", "session_id")
			return
		}
		var body struct {
			Confirm    bool `json:"confirm"`
			DropMemory bool `json:"drop_memory"`
		}
		if !decode(w, r, &body) {
			return
		}
		inventory, err := s.writers.SessionInventory(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "session_id")
			return
		}
		if !body.Confirm {
			writeJSON(w, http.StatusOK, map[string]any{"session_id": id, "inventory": inventory})
			return
		}
		inventory, err = s.registry.Delete(id)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error(), "session_id")
			return
		}
		if s.projector != nil {
			s.projector.Delete(id)
		}
		dropped := 0
		if body.DropMemory && s.memoryState != nil {
			dropped, err = s.memoryState.DropSessionWrites(inventory.MemoryWrites)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error(), "memory")
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"session_id": id, "inventory": inventory, "memory_entries_dropped": dropped})
		return
	}
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Label    *string `json:"label"`
			AgentID  *string `json:"agent_id"`
			ServerID *string `json:"server_id"`
		}
		if !decode(w, r, &body) {
			return
		}
		if body.Label == nil && body.AgentID == nil && body.ServerID == nil {
			writeError(w, 400, "label or agent_id is required", "session")
			return
		}
		if body.AgentID == nil && body.ServerID != nil {
			s.mu.RLock()
			for _, candidate := range s.cfg.Agents {
				if candidate.B == *body.ServerID {
					value := config.AgentID(candidate.Name)
					body.AgentID = &value
					break
				}
			}
			s.mu.RUnlock()
		}
		if body.AgentID != nil {
			if err := s.registry.SetAgent(id, *body.AgentID); err != nil {
				status := http.StatusBadRequest
				field := "agent_id"
				if strings.Contains(err.Error(), "not found") {
					status, field = http.StatusNotFound, "session"
				} else if strings.Contains(err.Error(), "running") || strings.Contains(err.Error(), "closed") {
					status, field = http.StatusConflict, "session"
				}
				writeError(w, status, err.Error(), field)
				return
			}
		}
		if body.Label != nil {
			if err := s.registry.Rename(id, *body.Label); err != nil {
				status := http.StatusNotFound
				if strings.Contains(err.Error(), "closed") {
					status = http.StatusConflict
				}
				writeError(w, status, err.Error(), "session")
				return
			}
		}
		item, ok := s.registry.Get(id)
		if !ok {
			writeError(w, 404, "session not found", "session")
			return
		}
		if body.AgentID != nil && s.runner != nil {
			s.runner.PublishBudget(r.Context(), item)
		}
		writeJSON(w, 200, map[string]any{"session": item.Snapshot()})
	case http.MethodDelete:
		err := s.registry.Close(id)
		if err != nil {
			status := 404
			if strings.Contains(err.Error(), "running") || strings.Contains(err.Error(), "closed") {
				status = 409
			}
			writeError(w, status, err.Error(), "session")
			return
		}
		if s.runner != nil {
			s.runner.LapseSessionGrants(id)
		}
		if s.operatorFiles != nil {
			if item, ok := s.registry.Get(id); ok {
				if path, err := s.operatorFiles.ExportChat(item.Snapshot()); err != nil {
					s.bus.Publish(events.New(events.Error, id, "", map[string]any{"where": "chat_export", "message": err.Error()}))
				} else {
					s.bus.Publish(events.New(events.ChatExported, id, "", map[string]any{"path": path}))
				}
			}
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
		assigned := false
		for _, agent := range s.cfg.Agents {
			if agent.B == tail || agent.C == tail || agent.D == tail {
				assigned = true
				break
			}
		}
		if assigned {
			s.mu.Unlock()
			writeError(w, 409, "profile is assigned to an agent role", "agents")
			return
		}
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
	s.probeMu.Lock()
	if prior := s.probeCancels[id]; prior != nil {
		prior.cancel()
	}
	probeContext, cancel := context.WithCancel(context.Background())
	current := &probeRun{cancel: cancel}
	s.probeCancels[id] = current
	s.probeMu.Unlock()
	go s.runProbe(probeContext, profile, current)
	writeJSON(w, 202, map[string]string{"status": "probing", "server_id": id})
}
func (s *Server) runProbe(ctx context.Context, profile *config.Profile, current *probeRun) {
	caps, findings, err := probe.Probe(ctx, profile)
	probeSucceeded := err == nil
	s.clearProbe(profile.ID, current)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		caps, findings = failedProbeCapabilities(profile, err)
	}
	s.mu.Lock()
	for i := range s.cfg.Servers {
		if s.cfg.Servers[i].ID == profile.ID {
			s.cfg.Servers[i].Capabilities = caps
			s.cfg.Servers[i].Reasoning.ValidEfforts = append([]string(nil), caps.ValidEfforts...)
			if s.cfg.Servers[i].Context.NCtx == 0 {
				s.cfg.Servers[i].Context.NCtx = caps.NCtx
			}
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
	if probeSucceeded && s.scheduler != nil {
		s.scheduler.ReleaseModel(profile.ID)
	}
}

func (s *Server) clearProbe(profileID string, current *probeRun) {
	s.probeMu.Lock()
	if s.probeCancels[profileID] == current {
		delete(s.probeCancels, profileID)
	}
	s.probeMu.Unlock()
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
		if err := config.ResolveProfileCredentials(&next, s.roots.Data); err != nil {
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
		SessionID   string              `json:"session_id"`
		Text        string              `json:"text"`
		Attachments []events.Attachment `json:"attachments,omitempty"`
	}
	if !decode(w, r, &body) {
		return
	}
	attachments, err := s.validateMessageAttachments(body.SessionID, body.Attachments)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "attachments")
		return
	}
	if strings.TrimSpace(body.Text) == "" && len(attachments) == 0 {
		writeError(w, http.StatusBadRequest, "text or attachments required", "text")
		return
	}
	s.bindMu.Lock()
	_, waitingForBind := s.pendingBinds[body.SessionID]
	s.bindMu.Unlock()
	if waitingForBind {
		writeError(w, http.StatusConflict, "answer the pending workspace binding decision first", "session_id")
		return
	}
	if len(attachments) == 0 {
		if item, ok := s.registry.Get(body.SessionID); ok {
			if dir := namedOutsideDirectory(body.Text, item.Snapshot().WorkspaceDir); dir != "" {
				s.bindMu.Lock()
				s.pendingBinds[body.SessionID] = pendingBind{Dir: dir, Text: body.Text}
				s.bindMu.Unlock()
				s.bus.Publish(events.New(events.WorkspaceBindRequired, body.SessionID, "", map[string]any{"dir": dir}))
				writeJSON(w, http.StatusAccepted, map[string]any{"bind_offer": true, "dir": dir})
				return
			}
		}
	}
	result, err := s.scheduler.SubmitAttachments(r.Context(), body.SessionID, body.Text, attachments)
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

func (s *Server) bindWorkspace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		method(w)
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
		Decision  string `json:"decision"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Decision != "yes" && body.Decision != "no" {
		writeError(w, http.StatusBadRequest, "decision must be yes or no", "decision")
		return
	}
	s.bindMu.Lock()
	pending, ok := s.pendingBinds[body.SessionID]
	if ok {
		delete(s.pendingBinds, body.SessionID)
	}
	s.bindMu.Unlock()
	if !ok {
		writeError(w, http.StatusConflict, "no workspace binding decision is pending", "session_id")
		return
	}
	if body.Decision == "yes" {
		item, err := s.registry.BindWorkspace(body.SessionID, pending.Dir)
		if err != nil {
			s.bindMu.Lock()
			s.pendingBinds[body.SessionID] = pending
			s.bindMu.Unlock()
			writeError(w, http.StatusConflict, err.Error(), "workspace")
			return
		}
		if s.runner != nil {
			s.runner.PublishBudget(r.Context(), item)
		}
	}
	s.bus.Publish(events.New(events.WorkspaceBindDecided, body.SessionID, "", map[string]any{"dir": pending.Dir, "decision": body.Decision}))
	result, err := s.scheduler.SubmitAttachments(r.Context(), body.SessionID, pending.Text, pending.Attachments)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error(), "session_id")
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func namedOutsideDirectory(text, bound string) string {
	for _, candidate := range windowsPathCandidates(text) {
		for candidate != "" {
			candidate = strings.TrimSpace(strings.TrimRight(candidate, ".,;:!?)]}"))
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				rel, relErr := filepath.Rel(bound, candidate)
				if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
					return filepath.Clean(candidate)
				}
				break
			}
			cut := strings.LastIndexAny(candidate, " \t")
			if cut < 0 {
				break
			}
			candidate = candidate[:cut]
		}
	}
	return ""
}

func windowsPathCandidates(text string) []string {
	values := []string{}
	for index := 0; index+3 <= len(text); index++ {
		letter := (text[index] >= 'A' && text[index] <= 'Z') || (text[index] >= 'a' && text[index] <= 'z')
		if letter && text[index+1] == ':' && (text[index+2] == '\\' || text[index+2] == '/') {
			end := index + 3
			for end < len(text) && !strings.ContainsRune("\r\n\"'`<>|", rune(text[end])) {
				end++
			}
			values = append(values, text[index:end])
			index = end - 1
		}
	}
	return values
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
	s.cancelProbes(body.SessionID, body.All)
	writeJSON(w, 200, map[string]any{"stopped": s.scheduler.Stop(body.SessionID, body.All)})
}

func (s *Server) cancelProbes(sessionID string, all bool) {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	if all {
		for _, probe := range s.probeCancels {
			probe.cancel()
		}
		return
	}
	if item, ok := s.registry.Get(sessionID); ok {
		if probe := s.probeCancels[item.ServerID]; probe != nil {
			probe.cancel()
		}
	}
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
	var before func()
	if body.Decision == "operator_mode" {
		if err := s.operatorRequest(r); err != nil {
			writeError(w, http.StatusForbidden, "operator mode can be enabled only by a local process owned by the Windows account that launched Agent_b", "decision")
			return
		}
		before = func() { s.setOperatorContext(true, "enabled from shell approval card", 0) }
	}
	if err := s.runner.Gate().DecideWith(body.SessionID, body.CallID, body.Decision, before); err != nil {
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
		AgentID   string `json:"agent_id"`
		Enabled   bool   `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AgentID == "" {
		item, ok := s.registry.Get(body.SessionID)
		if !ok {
			writeError(w, 404, "session not found", "session_id")
			return
		}
		if item.IsClosed() {
			writeError(w, 409, "session is closed", "session_id")
			return
		}
		body.AgentID = item.AgentID
	}
	s.mu.Lock()
	foundAgent, foundTool := false, false
	for i := range s.cfg.Agents {
		if config.AgentID(s.cfg.Agents[i].Name) != body.AgentID {
			continue
		}
		foundAgent = true
		selected := map[string]bool{}
		for _, tool := range s.cfg.Agents[i].Toolset {
			selected[tool] = true
		}
		if _, known := selected[name]; known {
			foundTool = true
		}
		for _, known := range config.FullToolset() {
			if known == name {
				foundTool = true
			}
		}
		if body.Enabled {
			selected[name] = true
		} else {
			delete(selected, name)
		}
		next := []string{}
		for _, known := range config.FullToolset() {
			if selected[known] {
				next = append(next, known)
			}
		}
		s.cfg.Agents[i].Toolset = next
		break
	}
	var saveErr error
	if foundAgent && foundTool {
		saveErr = s.cfg.Save(s.configPath)
	}
	s.mu.Unlock()
	if !foundAgent {
		writeError(w, 404, "agent not found", "agent_id")
		return
	}
	if !foundTool {
		writeError(w, 404, "tool not found", "name")
		return
	}
	if saveErr != nil {
		writeError(w, 500, saveErr.Error(), "config")
		return
	}
	if s.runner != nil {
		s.runner.Configure(s.ConfigSnapshot())
	}
	enabled := map[string]bool{}
	cfg := s.ConfigSnapshot()
	agent, _ := cfg.Agent(body.AgentID)
	for _, tool := range agent.Toolset {
		enabled[tool] = true
	}
	s.registry.ApplyAgentToolset(body.AgentID, enabled)
	for _, affected := range s.registry.List() {
		if affected.AgentID == body.AgentID {
			s.bus.Publish(events.New(events.ToolToggled, affected.ID, "", map[string]any{"agent_id": body.AgentID, "name": name, "enabled": body.Enabled}))
			s.runner.PublishBudget(r.Context(), affected)
		}
	}
	writeJSON(w, 200, map[string]any{"agent_id": body.AgentID, "name": name, "enabled": body.Enabled})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/stats/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" || s.statsState == nil {
		writeError(w, http.StatusNotFound, "agent stats not found", "agent_id")
		return
	}
	agentID := parts[0]
	if _, ok := s.ConfigSnapshot().Agent(agentID); !ok {
		writeError(w, http.StatusNotFound, "agent not found", "agent_id")
		return
	}
	if r.Method == http.MethodGet && len(parts) == 1 {
		writeJSON(w, http.StatusOK, s.statsState.Snapshot(agentID))
		return
	}
	if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "clear" {
		if err := s.operatorRequest(r); err != nil {
			writeError(w, http.StatusForbidden, "clearing stats requires a verified local operator process", "agent_id")
			return
		}
		var body struct {
			Confirm bool `json:"confirm"`
		}
		if !decode(w, r, &body) {
			return
		}
		if !body.Confirm {
			writeError(w, http.StatusBadRequest, "confirmation is required", "confirm")
			return
		}
		if err := s.statsState.Clear(agentID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "stats")
			return
		}
		s.bus.Publish(events.New(events.StatsCleared, "", "", map[string]any{"agent_id": agentID}))
		writeJSON(w, http.StatusOK, s.statsState.Snapshot(agentID))
		return
	}
	method(w)
}

func (s *Server) agentAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/agents/"), "/"), "/")
	if len(parts) != 3 || parts[1] != "memory" || parts[2] != "flush" || r.Method != http.MethodPost {
		method(w)
		return
	}
	if err := s.operatorRequest(r); err != nil {
		writeError(w, http.StatusForbidden, "flushing memory requires a verified local operator process", "agent_id")
		return
	}
	if s.memoryState == nil || s.registry == nil {
		writeError(w, http.StatusServiceUnavailable, "memory runtime unavailable", "memory")
		return
	}
	agentID := parts[0]
	if _, ok := s.ConfigSnapshot().Agent(agentID); !ok {
		writeError(w, http.StatusNotFound, "agent not found", "agent_id")
		return
	}
	var body struct {
		Workspace string `json:"workspace"`
		Confirm   bool   `json:"confirm"`
	}
	if !decode(w, r, &body) {
		return
	}
	workspace := filepath.Clean(strings.TrimSpace(body.Workspace))
	if workspace == "." || !filepath.IsAbs(workspace) {
		writeError(w, http.StatusBadRequest, "workspace must be an absolute path", "workspace")
		return
	}
	agentEntries, err := s.memoryState.CountAgent(agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "memory")
		return
	}
	workspaceEntries, err := s.memoryState.Count(workspace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "memory")
		return
	}
	if !body.Confirm {
		writeJSON(w, http.StatusOK, map[string]any{"agent_id": agentID, "workspace": workspace, "agent_entries": agentEntries, "workspace_entries": workspaceEntries})
		return
	}
	if err := s.memoryState.ClearAgent(agentID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "memory")
		return
	}
	if err := s.memoryState.Clear(workspace); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "memory")
		return
	}
	s.registry.ClearAgentMemory(agentID)
	s.registry.ClearWorkspaceMemory(workspace)
	s.bus.Publish(events.New(events.MemoryFlushed, "", "", map[string]any{"agent_id": agentID, "workspace": workspace, "agent_entries": agentEntries, "workspace_entries": workspaceEntries}))
	writeJSON(w, http.StatusOK, map[string]any{"agent_id": agentID, "workspace": workspace, "agent_entries": agentEntries, "workspace_entries": workspaceEntries})
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
