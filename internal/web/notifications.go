package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"harness/internal/events"
	"harness/internal/notifications"
)

type notificationManager interface {
	Validate(string) error
	Configure(string) error
	State() notifications.State
	SendTest(context.Context) error
}

func (s *Server) notificationSettings(w http.ResponseWriter, r *http.Request) {
	if s.notifications == nil || s.notificationStore == nil {
		writeError(w, http.StatusConflict, "notification runtime is unavailable", "notifications")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.notifications.State())
	case http.MethodPost:
		if err := s.operatorRequest(r); err != nil {
			writeError(w, http.StatusForbidden, "notification settings can be changed only by the local Windows account that launched Agent_b", "notifications.discord_url")
			return
		}
		var body struct {
			Action string `json:"action"`
			URL    string `json:"url"`
		}
		if !decode(w, r, &body) {
			return
		}
		switch body.Action {
		case "save":
			if strings.TrimSpace(body.URL) == "" {
				if s.notifications.State().Configured {
					// A blank write is "keep" for a masked credential. Disabling is
					// explicit so merely opening and saving Settings cannot erase it.
					writeJSON(w, http.StatusOK, map[string]any{"ok": true, "notifications": s.notifications.State()})
					return
				}
				writeError(w, http.StatusBadRequest, "Discord webhook URL is required", "notifications.discord_url")
				return
			}
			if err := s.notifications.Validate(body.URL); err != nil {
				writeError(w, http.StatusBadRequest, err.Error(), "notifications.discord_url")
				return
			}
			if err := s.notificationStore.Write([]byte(strings.TrimSpace(body.URL))); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error(), "notifications.discord_url")
				return
			}
			if err := s.notifications.Configure(body.URL); err != nil {
				writeError(w, http.StatusInternalServerError, "stored Discord URL could not be activated", "notifications.discord_url")
				return
			}
		case "clear":
			if err := s.notificationStore.Clear(); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error(), "notifications.discord_url")
				return
			}
			_ = s.notifications.Configure("")
		case "test":
			ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
			defer cancel()
			if err := s.notifications.SendTest(ctx); err != nil {
				writeError(w, http.StatusBadGateway, err.Error(), "notifications.discord_url")
				return
			}
		default:
			writeError(w, http.StatusBadRequest, "action must be save, clear, or test", "action")
			return
		}
		state := s.notifications.State()
		s.bus.Publish(events.New(events.NotificationChanged, "", "", state))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "notifications": state})
	default:
		method(w)
	}
}
