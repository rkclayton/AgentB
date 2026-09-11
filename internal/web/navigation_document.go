package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"harness/internal/events"
)

type documentResponseWriter struct {
	http.ResponseWriter
	bytes int
}

func (w *documentResponseWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.bytes += n
	return n, err
}

func (s *Server) instrumentDocument(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request)) {
	arrival := time.Now().UTC()
	navigationID := r.URL.Query().Get("navigation_id")
	if !validNavigationID(navigationID) {
		navigationID = ""
	}
	requestID := navigationID
	if requestID == "" {
		requestID = fmt.Sprintf("document-%d", arrival.UnixNano())
	}
	sessionID := r.URL.Query().Get("session")
	connectionID := r.RemoteAddr
	entry := time.Now().UTC()
	s.bus.Publish(events.New(events.NavigationDocumentStarted, sessionID, "", map[string]any{
		"request_id": requestID, "navigation_id": navigationID, "path": r.URL.Path,
		"arrival_at": arrival.Format(time.RFC3339Nano), "handler_entered_at": entry.Format(time.RFC3339Nano),
		"connection_identity": connectionID,
	}))
	counted := &documentResponseWriter{ResponseWriter: w}
	defer func() {
		s.bus.Publish(events.New(events.NavigationDocumentCompleted, sessionID, "", map[string]any{
			"request_id": requestID, "navigation_id": navigationID, "path": r.URL.Path,
			"handler_exited_at": time.Now().UTC().Format(time.RFC3339Nano), "bytes_written": counted.bytes,
			"connection_identity": connectionID,
		}))
	}()
	next(counted, r)
}

func validNavigationID(value string) bool {
	return value != "" && len(value) <= 128 && !strings.ContainsAny(value, "\r\n\t")
}
