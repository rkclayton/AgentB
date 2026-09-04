package web

import (
	"net/http"
	"time"

	"harness/internal/events"
	"harness/internal/hardening"
)

type hardeningOperation struct {
	Action     string `json:"action,omitempty"`
	State      string `json:"state,omitempty"`
	Message    string `json:"message,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

type hardeningResponse struct {
	hardening.Status
	Operation hardeningOperation `json:"operation"`
}

func (s *Server) beginHardening(action, message string) {
	s.hardeningMu.Lock()
	s.hardeningOp = hardeningOperation{
		Action: action, State: "running", Message: message,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	s.hardeningMu.Unlock()
}

func (s *Server) progressHardening(message string) {
	s.hardeningMu.Lock()
	s.hardeningOp.Message = message
	s.hardeningMu.Unlock()
}

func (s *Server) finishHardening(state, message string) {
	s.hardeningMu.Lock()
	s.hardeningOp.State = state
	s.hardeningOp.Message = message
	s.hardeningOp.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	s.hardeningMu.Unlock()
	if state == "failed" && s.bus != nil {
		s.bus.Publish(events.New(events.Error, "", "", map[string]any{"where": "hardening", "message": message}))
	}
}

func (s *Server) hardeningOperation() hardeningOperation {
	s.hardeningMu.RLock()
	defer s.hardeningMu.RUnlock()
	return s.hardeningOp
}

func (s *Server) writeHardeningStatus(w http.ResponseWriter, status hardening.Status) {
	writeJSON(w, http.StatusOK, hardeningResponse{Status: status, Operation: s.hardeningOperation()})
}
