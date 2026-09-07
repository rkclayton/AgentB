package agent

import (
	"harness/internal/events"
	"harness/internal/session"
)

func fileGrantTool(name string) bool {
	switch name {
	case "read_file", "list_dir", "write_file", "edit_file", "search_text", "find_files":
		return true
	default:
		return false
	}
}

func (r *Runner) hasFileGrant(sessionID, runID string) bool {
	r.fileGrantMu.Lock()
	defer r.fileGrantMu.Unlock()
	return r.fileRunGrants[shellGrantKey(sessionID, runID)] || r.fileSessionGrants[sessionID] != ""
}

func (r *Runner) grantFileRun(s *session.Session, runID string) {
	key := shellGrantKey(s.ID, runID)
	r.fileGrantMu.Lock()
	if r.fileRunGrants == nil {
		r.fileRunGrants = map[string]bool{}
	}
	if r.fileRunGrants[key] {
		r.fileGrantMu.Unlock()
		return
	}
	r.fileRunGrants[key] = true
	r.fileGrantMu.Unlock()
	r.publishFileGrant(events.FileGrant, s.ID, runID, "run", "")
}

func (r *Runner) grantFileSession(s *session.Session, runID string) {
	r.fileGrantMu.Lock()
	if r.fileSessionGrants == nil {
		r.fileSessionGrants = map[string]string{}
	}
	if r.fileSessionGrants[s.ID] != "" {
		r.fileGrantMu.Unlock()
		return
	}
	r.fileSessionGrants[s.ID] = runID
	r.fileGrantMu.Unlock()
	r.publishFileGrant(events.FileGrant, s.ID, runID, "session", "")
}

func (r *Runner) lapseFileRunGrant(s *session.Session, runID string) {
	key := shellGrantKey(s.ID, runID)
	r.fileGrantMu.Lock()
	granted := r.fileRunGrants[key]
	delete(r.fileRunGrants, key)
	r.fileGrantMu.Unlock()
	if granted {
		r.publishFileGrant(events.FileGrantLapsed, s.ID, runID, "run", "run ended")
	}
}

func (r *Runner) publishFileGrant(eventType, sessionID, runID, scope, reason string) {
	if r.bus == nil {
		return
	}
	data := map[string]any{"run_id": runID, "scope": scope, "identity": "operator"}
	if reason != "" {
		data["reason"] = reason
	}
	r.bus.Publish(events.New(eventType, sessionID, runID, data))
}
