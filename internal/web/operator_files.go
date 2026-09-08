package web

import (
	"net/http"
	"os"
	"sort"
	"strings"

	"harness/internal/events"
	"harness/internal/tools"
)

func (s *Server) operatorAttachments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		method(w)
		return
	}
	if s.operatorFiles == nil {
		writeError(w, http.StatusNotImplemented, "operator files are unavailable", "operator_files")
		return
	}
	root := s.operatorFiles.AttachmentsPath()
	if selected := r.URL.Query().Get("path"); selected != "" {
		s.exchangeFile(w, r, root, selected)
		return
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		writeJSON(w, http.StatusOK, map[string]any{"files": []events.Attachment{}})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "attachments")
		return
	}
	files := []events.Attachment{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		resolved, resolveErr := tools.Resolve(root, entry.Name())
		if resolveErr != nil {
			continue
		}
		info, statErr := os.Stat(resolved)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		digest, hashErr := fileSHA256(resolved)
		if hashErr == nil {
			files = append(files, events.Attachment{Path: entry.Name(), Bytes: info.Size(), SHA256: digest})
		}
	}
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Path) < strings.ToLower(files[j].Path) })
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (s *Server) operatorFileState(w http.ResponseWriter, r *http.Request) {
	if s.operatorFiles == nil {
		writeError(w, http.StatusNotImplemented, "operator files are unavailable", "operator_files")
		return
	}
	switch r.Method {
	case http.MethodGet:
		status, err := s.operatorFiles.Status(r.URL.Query().Get("dir"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error(), "dir")
			return
		}
		writeJSON(w, http.StatusOK, status)
	case http.MethodPost:
		var body struct {
			Action         string `json:"action"`
			Dir            string `json:"dir"`
			Confirm        bool   `json:"confirm"`
			Cleanup        bool   `json:"cleanup"`
			ConfirmCleanup bool   `json:"confirm_cleanup"`
		}
		if !decode(w, r, &body) {
			return
		}
		switch body.Action {
		case "empty_attachments":
			if !body.Confirm {
				writeError(w, http.StatusBadRequest, "empty attachments requires confirmation", "confirm")
				return
			}
			files, bytes, err := s.operatorFiles.EmptyAttachments()
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error(), "attachments")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"files_removed": files, "bytes_removed": bytes})
		case "adopt_instructions":
			if body.Cleanup && !body.ConfirmCleanup {
				writeError(w, http.StatusBadRequest, "removing AGENTS.md / CLAUDE.md requires the cleanup checkbox", "confirm_cleanup")
				return
			}
			path, removed, err := s.operatorFiles.Adopt(body.Dir, body.Cleanup)
			if err != nil {
				writeError(w, http.StatusConflict, err.Error(), "dir")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"path": path, "removed": removed})
		default:
			writeError(w, http.StatusBadRequest, "action must be empty_attachments or adopt_instructions", "action")
		}
	default:
		method(w)
	}
}

func (s *Server) uiError(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		method(w)
		return
	}
	var body struct {
		SessionID   string `json:"session_id"`
		Kind        string `json:"kind"`
		Message     string `json:"message"`
		Stack       string `json:"stack"`
		RepeatCount int    `json:"repeat_count"`
		Capped      bool   `json:"capped"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Message) > 4096 {
		body.Message = body.Message[:4096]
	}
	if len(body.Stack) > 8192 {
		body.Stack = body.Stack[:8192]
	}
	if body.SessionID != "" && s.registry != nil {
		if _, ok := s.registry.Get(body.SessionID); !ok {
			body.SessionID = ""
		}
	}
	if body.RepeatCount < 1 {
		body.RepeatCount = 1
	}
	s.bus.Publish(events.New(events.Error, body.SessionID, "", map[string]any{"where": "ui", "kind": body.Kind, "message": body.Message, "stack": body.Stack, "repeat_count": body.RepeatCount, "capped": body.Capped}))
	w.WriteHeader(http.StatusNoContent)
}
