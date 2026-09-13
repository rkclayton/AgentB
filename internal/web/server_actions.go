package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"harness/internal/config"
	"harness/internal/events"
)

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
	if len(parts) == 2 && parts[1] == "server" {
		s.agentServer(w, r, parts[0])
		return
	}
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
