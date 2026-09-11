package web

import (
	"math"
	"net/http"
	"strings"
	"time"

	"harness/internal/events"
)

type navigationMeasurementBody struct {
	NavigationID                    string   `json:"navigation_id"`
	NavigationKind                  string   `json:"navigation_kind"`
	From                            string   `json:"from"`
	To                              string   `json:"to"`
	DocumentRequestParseMS          float64  `json:"document_request_parse_ms"`
	ModulePageInitMS                float64  `json:"module_page_init_ms"`
	SessionStateFetchMS             float64  `json:"session_state_fetch_ms"`
	EventStreamConnectMS            float64  `json:"event_stream_connect_ms"`
	TranscriptSurfaceRebuildPaintMS float64  `json:"transcript_surface_rebuild_paint_ms"`
	EndToEndMS                      float64  `json:"end_to_end_ms"`
	TranscriptEntries               int      `json:"transcript_entries"`
	ChatID                          string   `json:"chat_id"`
	ModelReachability               string   `json:"model_reachability"`
	SincePreviousNavigationMS       *float64 `json:"since_previous_navigation_ms"`
	InstrumentationSyncMS           float64  `json:"instrumentation_sync_ms"`
}

type navigationStartBody struct {
	NavigationID              string   `json:"navigation_id"`
	NavigationKind            string   `json:"navigation_kind"`
	From                      string   `json:"from"`
	To                        string   `json:"to"`
	FullDocument              bool     `json:"full_document"`
	ClickedAt                 float64  `json:"clicked_at"`
	ChatID                    string   `json:"chat_id"`
	SincePreviousNavigationMS *float64 `json:"since_previous_navigation_ms"`
}

type navigationSuppressionBody struct {
	SuppressionID  string  `json:"suppression_id"`
	NavigationKind string  `json:"navigation_kind"`
	From           string  `json:"from"`
	To             string  `json:"to"`
	ClickedAt      float64 `json:"clicked_at"`
	ChatID         string  `json:"chat_id"`
}

func (s *Server) navigationSuppression(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		method(w)
		return
	}
	var body navigationSuppressionBody
	if !decode(w, r, &body) {
		return
	}
	if !validNavigationSuppression(body) {
		writeError(w, http.StatusBadRequest, "invalid navigation suppression", "body")
		return
	}
	if body.ChatID != "" && s.registry != nil {
		if _, ok := s.registry.Get(body.ChatID); !ok {
			writeError(w, http.StatusNotFound, "chat not found", "chat_id")
			return
		}
	}
	if !s.claimNavigationID(events.NavigationSuppressed + "\x00" + body.SuppressionID) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.bus.Publish(events.New(events.NavigationSuppressed, body.ChatID, "", map[string]any{
		"suppression_id": body.SuppressionID, "navigation_kind": body.NavigationKind,
		"from": body.From, "to": body.To, "clicked_at": body.ClickedAt, "chat_id": body.ChatID,
	}))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) navigationStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		method(w)
		return
	}
	var body navigationStartBody
	if !decode(w, r, &body) {
		return
	}
	if !validNavigationStart(body) {
		writeError(w, http.StatusBadRequest, "invalid navigation start", "body")
		return
	}
	if body.ChatID != "" && s.registry != nil {
		if _, ok := s.registry.Get(body.ChatID); !ok {
			writeError(w, http.StatusNotFound, "chat not found", "chat_id")
			return
		}
	}
	if !s.claimNavigationID(events.NavigationStarted + "\x00" + body.NavigationID) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.bus.Publish(events.New(events.NavigationStarted, body.ChatID, "", map[string]any{
		"navigation_id": body.NavigationID, "navigation_kind": body.NavigationKind,
		"from": body.From, "to": body.To, "full_document": body.FullDocument,
		"clicked_at": body.ClickedAt, "chat_id": body.ChatID,
		"since_previous_navigation_ms": body.SincePreviousNavigationMS,
		"subscriber_count":             s.bus.SubscriberCount(),
	}))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) navigationMeasurement(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		method(w)
		return
	}
	var body navigationMeasurementBody
	if !decode(w, r, &body) {
		return
	}
	if !validNavigationMeasurement(body) {
		writeError(w, http.StatusBadRequest, "invalid navigation measurement", "body")
		return
	}
	if body.ChatID != "" && s.registry != nil {
		if _, ok := s.registry.Get(body.ChatID); !ok {
			writeError(w, http.StatusNotFound, "chat not found", "chat_id")
			return
		}
	}
	if !s.claimNavigationID(body.NavigationID) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	data := map[string]any{
		"navigation_id": body.NavigationID, "navigation_kind": body.NavigationKind,
		"from": body.From, "to": body.To,
		"document_request_parse_ms":           body.DocumentRequestParseMS,
		"module_page_init_ms":                 body.ModulePageInitMS,
		"session_state_fetch_ms":              body.SessionStateFetchMS,
		"event_stream_connect_ms":             body.EventStreamConnectMS,
		"transcript_surface_rebuild_paint_ms": body.TranscriptSurfaceRebuildPaintMS,
		"end_to_end_ms":                       body.EndToEndMS,
		"transcript_entries":                  body.TranscriptEntries,
		"chat_id":                             body.ChatID,
		"model_reachability":                  body.ModelReachability,
		"since_previous_navigation_ms":        body.SincePreviousNavigationMS,
		"instrumentation_sync_ms":             body.InstrumentationSyncMS,
	}
	s.bus.Publish(events.New(events.NavigationMeasured, body.ChatID, "", data))
	w.WriteHeader(http.StatusNoContent)
}

func validNavigationMeasurement(body navigationMeasurementBody) bool {
	if body.NavigationID == "" || len(body.NavigationID) > 128 || strings.ContainsAny(body.NavigationID, "\r\n\t") {
		return false
	}
	if body.NavigationKind != "flip" && body.NavigationKind != "settings" {
		return false
	}
	validSurface := func(value string) bool {
		return value == "chat" || value == "console" || value == "settings" || value == "plan"
	}
	if !validSurface(body.From) || !validSurface(body.To) || body.From == body.To {
		return false
	}
	if body.ModelReachability != "reachable" && body.ModelReachability != "unreachable" && body.ModelReachability != "unknown" {
		return false
	}
	if body.TranscriptEntries < 0 || body.TranscriptEntries > 10_000_000 || len(body.ChatID) > 256 {
		return false
	}
	for _, value := range []float64{body.DocumentRequestParseMS, body.ModulePageInitMS, body.SessionStateFetchMS, body.EventStreamConnectMS, body.TranscriptSurfaceRebuildPaintMS, body.EndToEndMS, body.InstrumentationSyncMS} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 600_000 {
			return false
		}
	}
	return body.SincePreviousNavigationMS == nil || (!math.IsNaN(*body.SincePreviousNavigationMS) && !math.IsInf(*body.SincePreviousNavigationMS, 0) && *body.SincePreviousNavigationMS >= 0 && *body.SincePreviousNavigationMS <= float64((366*24*time.Hour).Milliseconds()))
}

func validNavigationStart(body navigationStartBody) bool {
	if body.NavigationID == "" || len(body.NavigationID) > 128 || strings.ContainsAny(body.NavigationID, "\r\n\t") {
		return false
	}
	if body.NavigationKind != "flip" && body.NavigationKind != "settings" {
		return false
	}
	validSurface := func(value string) bool {
		return value == "chat" || value == "console" || value == "settings" || value == "plan"
	}
	if !validSurface(body.From) || !validSurface(body.To) || body.From == body.To || len(body.ChatID) > 256 {
		return false
	}
	if math.IsNaN(body.ClickedAt) || math.IsInf(body.ClickedAt, 0) || body.ClickedAt <= 0 {
		return false
	}
	return body.SincePreviousNavigationMS == nil || (!math.IsNaN(*body.SincePreviousNavigationMS) && !math.IsInf(*body.SincePreviousNavigationMS, 0) && *body.SincePreviousNavigationMS >= 0 && *body.SincePreviousNavigationMS <= float64((366*24*time.Hour).Milliseconds()))
}

func validNavigationSuppression(body navigationSuppressionBody) bool {
	if body.SuppressionID == "" || len(body.SuppressionID) > 128 || strings.ContainsAny(body.SuppressionID, "\r\n\t") {
		return false
	}
	if body.NavigationKind != "flip" || len(body.ChatID) > 256 {
		return false
	}
	if (body.From != "chat" && body.From != "console") || (body.To != "chat" && body.To != "console") || body.From == body.To {
		return false
	}
	return !math.IsNaN(body.ClickedAt) && !math.IsInf(body.ClickedAt, 0) && body.ClickedAt > 0
}

func (s *Server) claimNavigationID(id string) bool {
	s.navigationMu.Lock()
	defer s.navigationMu.Unlock()
	now := time.Now()
	for key, seen := range s.navigationIDs {
		if now.Sub(seen) > 24*time.Hour {
			delete(s.navigationIDs, key)
		}
	}
	if _, exists := s.navigationIDs[id]; exists {
		return false
	}
	s.navigationIDs[id] = now
	return true
}
