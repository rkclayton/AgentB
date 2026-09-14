package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/probe"
)

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
	s.startProbe(profile)
	writeJSON(w, 202, map[string]string{"status": "probing", "server_id": id})
}
func (s *Server) startProbe(profile *config.Profile) {
	s.cancelScheduledReachabilityProbe(profile.ID)
	s.probeMu.Lock()
	if prior := s.probeCancels[profile.ID]; prior != nil {
		prior.cancel()
	}
	probeContext, cancel := context.WithCancel(context.Background())
	current := &probeRun{cancel: cancel}
	s.probeCancels[profile.ID] = current
	s.probeMu.Unlock()
	go s.runProbe(probeContext, profile, current)
}
func (s *Server) runProbe(ctx context.Context, profile *config.Profile, current *probeRun) {
	caps, findings, err := probe.Probe(ctx, profile)
	probeSucceeded := err == nil
	wasCurrent := s.clearProbe(profile.ID, current)
	if ctx.Err() != nil {
		if wasCurrent {
			s.completeReachabilityProbe(profile.ID, false)
		}
		return
	}
	s.completeReachabilityProbe(profile.ID, probeSucceeded)
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

func (s *Server) clearProbe(profileID string, current *probeRun) bool {
	s.probeMu.Lock()
	cleared := false
	if s.probeCancels[profileID] == current {
		delete(s.probeCancels, profileID)
		cleared = true
	}
	s.probeMu.Unlock()
	return cleared
}

func failedProbeCapabilities(profile *config.Profile, err error) (config.Capabilities, []string) {
	caps := profile.Capabilities
	message := err.Error()
	detail := ""
	if friendly, ok := err.(interface {
		OperatorMessage() string
		Diagnostic() string
	}); ok {
		message, detail = friendly.OperatorMessage(), friendly.Diagnostic()
	}
	findings := []string{"probe failed: " + message}
	if detail != "" {
		findings = append(findings, "probe detail: "+detail)
	}
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
		if field := directNetworkPolicyField(patch); field != "" {
			writeError(w, http.StatusBadRequest, "apply the LAN policy through Settings > Security so Windows policy is verified before configuration is saved", field)
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
