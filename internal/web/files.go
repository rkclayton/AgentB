package web

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"harness/internal/tools"
)

func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		method(w)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/files/")
	if path == "" || strings.HasSuffix(path, "/") {
		http.NotFound(w, r)
		return
	}
	workspace, ok := s.fileWorkspace(r.URL.Query().Get("session"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	resolved, err := tools.Resolve(workspace, path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(resolved)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(resolved)})
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, filepath.Base(resolved), info.ModTime(), file)
}

func (s *Server) fileWorkspace(sessionID string) (string, bool) {
	if sessionID == "" {
		return s.roots.Workspace, s.roots.Workspace != ""
	}
	if s.registry != nil {
		if item, ok := s.registry.Get(sessionID); ok {
			if item.Role == "d" && item.PlanDir != "" {
				return item.PlanDir, true
			}
			return item.Workspace, true
		}
	}
	if s.replay != nil {
		if item, ok := s.replay.Sessions[sessionID]; ok {
			if item.Role == "d" && item.PlanDir != "" {
				return item.PlanDir, true
			}
			return item.Workspace, item.Workspace != ""
		}
	}
	return "", false
}

func (s *Server) openFileFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		method(w)
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
		Path      string `json:"path"`
		Scope     string `json:"scope"`
	}
	if !decode(w, r, &body) {
		return
	}
	root := ""
	if body.Scope == "" || body.Scope == "workspace" {
		var ok bool
		root, ok = s.fileWorkspace(body.SessionID)
		if !ok {
			writeError(w, http.StatusNotFound, "session not found", "session_id")
			return
		}
	} else if body.Scope == "exchange" {
		var err error
		root, err = s.ConfigSnapshot().ResolvedExchangeFolder()
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error(), "deliver.exchange_folder")
			return
		}
	} else {
		writeError(w, http.StatusBadRequest, "scope must be folder or exchange", "scope")
		return
	}
	resolved, err := tools.Resolve(root, body.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "file not found", "path")
		return
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "file not found", "path")
		return
	}
	if err := s.openFolder(resolved); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "path")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": body.Path})
}

func openContainingFolder(path string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("explorer.exe", "/select,"+path)
	case "darwin":
		command = exec.Command("open", "-R", path)
	default:
		command = exec.Command("xdg-open", filepath.Dir(path))
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("open containing folder: %w", err)
	}
	return command.Process.Release()
}
