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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"harness/internal/agent"
	"harness/internal/config"
	"harness/internal/credential"
	"harness/internal/detection"
	"harness/internal/events"
	"harness/internal/hardening"
	"harness/internal/memory"
	"harness/internal/ocr"
	"harness/internal/operatorfiles"
	"harness/internal/projection"
	"harness/internal/serviceaccount"
	"harness/internal/session"
	"harness/internal/signing"
	"harness/internal/stats"
	"harness/internal/tools"
	workspaceinfo "harness/internal/workspace"
)

type Server struct {
	mu                sync.RWMutex
	cfg               *config.Config
	configPath        string
	roots             RuntimeRoots
	bus               *events.Bus
	registry          *session.Registry
	webDir            string
	scheduler         *agent.Scheduler
	runner            *agent.Runner
	prompt            *agent.PromptRenderer
	replay            *projection.Replay
	projector         *projection.Store
	writers           *events.Writers
	credential        *credential.Store
	notifications     notificationManager
	notificationStore *credential.Store
	shell             *tools.Shell
	account           serviceaccount.Manager
	hardening         hardening.Manager
	hardeningMu       sync.RWMutex
	hardeningOp       hardeningOperation
	signing           signing.Manager
	signingMu         sync.Mutex
	signingStatus     signing.Status
	shellTest         func(context.Context) (string, error)
	accountMu         sync.Mutex
	mutationToken     string
	operatorChangeMu  sync.Mutex
	operatorMu        sync.Mutex
	operatorEnabled   bool
	operatorExpires   string
	operatorTimer     operatorTimer
	operatorEpoch     uint64
	operatorRequest   func(*http.Request) error
	operatorNow       func() time.Time
	operatorAfter     func(time.Duration, func()) operatorTimer
	openFolder        func(string) error
	extractClient     *http.Client
	ocrExtract        func(string) (string, error)
	detectLocal       func(context.Context, string) (any, error)
	workspaceState    *workspaceinfo.Manager
	memoryState       *memory.Manager
	statsState        *stats.Manager
	operatorFiles     *operatorfiles.Manager
	probeMu           sync.Mutex
	probeCancels      map[string]*probeRun
	reachabilityMu    sync.Mutex
	reachability      map[string]*reachabilityRetry
	reachabilityAfter func(time.Duration, func()) operatorTimer
	navigationMu      sync.Mutex
	navigationIDs     map[string]time.Time
	agentServerMu     sync.Mutex
	agentServers      map[string]pendingAgentServer
	tryAgentIdle      func(string) bool
}

type probeRun struct{ cancel context.CancelFunc }
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
		openFolder:   openContainingFolder,
		probeCancels: map[string]*probeRun{},
		reachability: map[string]*reachabilityRetry{},
		reachabilityAfter: func(duration time.Duration, fn func()) operatorTimer {
			return time.AfterFunc(duration, fn)
		},
		navigationIDs: map[string]time.Time{},
		agentServers:  map[string]pendingAgentServer{},
		extractClient: &http.Client{},
		ocrExtract:    ocr.Extract,
		detectLocal: func(ctx context.Context, account string) (any, error) {
			return detection.Local(ctx, filepath.Join(roots.Application, "scripts", "detect-local-capabilities.ps1"), account)
		},
	}
}
func (s *Server) SetRegistry(registry *session.Registry) {
	registry.SetPlansRoot(filepath.Join(s.roots.Data, "plans"))
	s.registry = registry
}
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
func (s *Server) SetNotifications(manager notificationManager, store *credential.Store) {
	s.notifications, s.notificationStore = manager, store
}
func (s *Server) SetServiceAccountManager(manager serviceaccount.Manager) { s.account = manager }
func (s *Server) SetHardeningManager(manager hardening.Manager)           { s.hardening = manager }
func (s *Server) SetSigningManager(manager signing.Manager)               { s.signing = manager }
func (s *Server) SetRuntime(scheduler *agent.Scheduler, runner *agent.Runner, prompt *agent.PromptRenderer) {
	s.scheduler = scheduler
	s.runner = runner
	s.prompt = prompt
	if scheduler != nil {
		scheduler.SetAgentIdleCallback(s.applyPendingAgentServer)
		s.tryAgentIdle = scheduler.TryAgentIdle
	}
	if runner != nil {
		runner.SetModelUnreachable(func(sessionID, profileID string) {
			if scheduler != nil {
				scheduler.HoldModel(sessionID)
			}
			s.scheduleReachabilityProbe(profileID)
		})
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
	mux.HandleFunc("/api/plans", s.replayGuard(s.plans))
	mux.HandleFunc("/api/plan", s.replayGuard(s.planSurface))
	mux.HandleFunc("/api/plan/accept", s.replayGuard(s.planAccept))
	mux.HandleFunc("/api/plan/marker", s.replayGuard(s.planMarker))
	mux.HandleFunc("/api/sessions/", s.replayGuard(s.session))
	mux.HandleFunc("/api/workspaces", s.replayGuard(s.workspaces))
	mux.HandleFunc("/api/workspaces/", s.replayGuard(s.workspaceAction))
	mux.HandleFunc("/api/servers", s.servers)
	mux.HandleFunc("/api/servers/", s.replayGuard(s.server))
	mux.HandleFunc("/api/config", s.replayGuard(s.config))
	mux.HandleFunc("/api/notifications", s.replayGuard(s.notificationSettings))
	mux.HandleFunc("/api/shell-credential", s.replayGuard(s.shellCredential))
	mux.HandleFunc("/api/service-account", s.replayGuard(s.serviceAccount))
	mux.HandleFunc("/api/hardening", s.replayGuard(s.hostHardening))
	mux.HandleFunc("/api/signing", s.replayGuard(s.codeSigning))
	mux.HandleFunc("/api/message", s.replayGuard(s.message))
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
		ModelAddress: host, ModelPort: port, AllowLocalNetwork: cfg.Shell.AllowLocalNetwork,
		LocalSubnets: append([]string(nil), cfg.Shell.ConfirmedLocalSubnets...),
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
