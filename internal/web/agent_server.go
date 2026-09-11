package web

import (
	"net/http"
	"time"

	"harness/internal/config"
	"harness/internal/events"
)

type pendingAgentServer struct {
	AgentID     string `json:"agent_id"`
	From        string `json:"from"`
	To          string `json:"to"`
	RequestedAt string `json:"requested_at"`
}

func (s *Server) agentServerChanges() map[string]pendingAgentServer {
	s.agentServerMu.Lock()
	defer s.agentServerMu.Unlock()
	result := make(map[string]pendingAgentServer, len(s.agentServers))
	for id, change := range s.agentServers {
		result[id] = change
	}
	return result
}

func (s *Server) agentServer(w http.ResponseWriter, r *http.Request, agentID string) {
	if r.Method != http.MethodPost {
		method(w)
		return
	}
	var body struct {
		Action   string `json:"action"`
		ServerID string `json:"server_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Action == "cancel" {
		s.agentServerMu.Lock()
		change, exists := s.agentServers[agentID]
		if exists {
			delete(s.agentServers, agentID)
		}
		s.agentServerMu.Unlock()
		if !exists {
			writeError(w, http.StatusNotFound, "no pending server change", "agent_id")
			return
		}
		s.bus.Publish(events.New(events.AgentServerChange, "", "", map[string]any{"status": "cancelled", "agent_id": agentID, "from": change.From, "to": change.To}))
		writeJSON(w, http.StatusOK, map[string]any{"status": "cancelled", "agent_id": agentID})
		return
	}
	if body.Action != "set" || body.ServerID == "" {
		writeError(w, http.StatusBadRequest, "action must be set with server_id, or cancel", "action")
		return
	}
	agent, ok := s.ConfigSnapshot().Agent(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found", "agent_id")
		return
	}
	if s.registry == nil {
		writeError(w, http.StatusServiceUnavailable, "agent runtime unavailable", "agent_id")
		return
	}
	if runnable, reason := s.registry.ProfileRunnable(body.ServerID); !runnable {
		writeError(w, http.StatusBadRequest, reason, "server_id")
		return
	}
	if body.ServerID == agent.B {
		s.agentServerMu.Lock()
		change, pending := s.agentServers[agentID]
		if pending {
			delete(s.agentServers, agentID)
		}
		s.agentServerMu.Unlock()
		if pending {
			s.bus.Publish(events.New(events.AgentServerChange, "", "", map[string]any{"status": "cancelled", "agent_id": agentID, "from": change.From, "to": change.To}))
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "applied", "agent_id": agentID, "server_id": body.ServerID})
		return
	}
	change := pendingAgentServer{AgentID: agentID, From: agent.B, To: body.ServerID, RequestedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	s.agentServerMu.Lock()
	s.agentServers[agentID] = change
	s.agentServerMu.Unlock()
	s.bus.Publish(events.New(events.AgentServerChange, "", "", map[string]any{"status": "pending", "change": change}))
	if s.tryAgentIdle != nil {
		s.tryAgentIdle(agentID)
	}
	if _, pending := s.agentServerChanges()[agentID]; pending {
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "pending", "change": change})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "applied", "agent_id": agentID, "server_id": body.ServerID})
}

func (s *Server) applyPendingAgentServer(agentID string) {
	s.agentServerMu.Lock()
	change, exists := s.agentServers[agentID]
	s.agentServerMu.Unlock()
	if !exists {
		return
	}

	s.mu.Lock()
	index := -1
	for i := range s.cfg.Agents {
		if config.AgentID(s.cfg.Agents[i].Name) == agentID {
			index = i
			break
		}
	}
	if index < 0 {
		s.mu.Unlock()
		s.bus.Publish(events.New(events.Error, "", "", map[string]any{"where": "agent_server_change", "message": "agent not found: " + agentID}))
		return
	}
	previous := s.cfg.Agents[index].B
	s.cfg.Agents[index].B = change.To
	if err := s.cfg.Save(s.configPath); err != nil {
		s.cfg.Agents[index].B = previous
		s.mu.Unlock()
		s.bus.Publish(events.New(events.Error, "", "", map[string]any{"where": "agent_server_change", "message": err.Error()}))
		return
	}
	masked := s.cfg.Masked()
	s.mu.Unlock()
	if s.runner != nil {
		s.runner.Configure(s.ConfigSnapshot())
	}
	if s.registry != nil {
		if err := s.registry.ApplyAgentBinding(agentID); err != nil {
			s.bus.Publish(events.New(events.Error, "", "", map[string]any{"where": "agent_server_change", "message": err.Error()}))
		}
	}
	s.agentServerMu.Lock()
	if current, ok := s.agentServers[agentID]; ok && current.To == change.To {
		delete(s.agentServers, agentID)
	}
	s.agentServerMu.Unlock()
	s.bus.Publish(events.New(events.ConfigChanged, "", "", map[string]any{"config": masked}))
	s.bus.Publish(events.New(events.AgentServerChange, "", "", map[string]any{"status": "applied", "agent_id": agentID, "from": change.From, "to": change.To}))
}
