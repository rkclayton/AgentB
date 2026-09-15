package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"harness/internal/events"
	"harness/internal/session"
)

var planProposalID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type planProposal = events.PlanProposal

func (s *Server) planSurface(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	item, ok := s.registry.Get(sessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found", "session_id")
		return
	}
	if r.Method != http.MethodGet {
		method(w)
		return
	}
	planDir, err := s.planDirFor(item)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error(), "plan")
		return
	}
	item.SetPlanPage(true)
	plan, err := os.ReadFile(filepath.Join(planDir, "plan.md"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "plan")
		return
	}
	notes, err := os.ReadFile(filepath.Join(planDir, "NOTES.md"))
	if os.IsNotExist(err) {
		notes = nil
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "notes")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan": string(plan), "notes": string(notes), "plan_id": filepath.Base(planDir), "fallback": item.Role != "d"})
}

func (s *Server) planAccept(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		method(w)
		return
	}
	var body struct {
		SessionID string       `json:"session_id"`
		Proposal  planProposal `json:"proposal"`
	}
	if !decode(w, r, &body) {
		return
	}
	item, ok := s.registry.Get(body.SessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found", "session_id")
		return
	}
	if err := validatePlanProposal(body.Proposal); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "proposal")
		return
	}
	if !recordedPlanProposal(item, body.Proposal) {
		writeError(w, http.StatusConflict, "proposal is not present in this session's tray", "proposal")
		return
	}
	result, err := s.acceptPlanProposal(r, item, body.Proposal)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error(), "proposal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": body.Proposal.ID, "result": result})
}

func (s *Server) planMarker(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		method(w)
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
		Line      string `json:"line"`
		Marker    string `json:"marker"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Marker != "x" && body.Marker != " " {
		writeError(w, http.StatusBadRequest, "marker may only be x or space", "marker")
		return
	}
	current := "x"
	if body.Marker == "x" {
		current = " "
	}
	if !strings.Contains(body.Line, "["+current+"]") {
		writeError(w, http.StatusBadRequest, "cube can only flip a filled or reopened marker", "line")
		return
	}
	proposal := planProposal{ID: "cube", Kind: "reword", Path: "plan.md", OldText: body.Line, NewText: strings.Replace(body.Line, "["+current+"]", "["+body.Marker+"]", 1), ItemID: "marker"}
	item, ok := s.registry.Get(body.SessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found", "session_id")
		return
	}
	result, err := s.acceptPlanProposal(r, item, proposal)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error(), "proposal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": proposal.ID, "result": result})
}

func validatePlanProposal(value planProposal) error {
	if !planProposalID.MatchString(value.ID) || !planProposalID.MatchString(value.ItemID) {
		return fmt.Errorf("proposal and item ids must be short identifiers")
	}
	allowed := map[string]bool{"add": true, "reword": true, "reorder": true, "drop": true, "agent_b_addition": true}
	if !allowed[value.Kind] {
		return fmt.Errorf("unsupported proposal kind")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value.Path)))
	if clean != value.Path || filepath.IsAbs(filepath.FromSlash(value.Path)) || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("proposal path must be a clean relative plan path")
	}
	planPath := clean == "plan.md" || (strings.HasPrefix(clean, "plan/items/") && strings.HasSuffix(clean, ".md") && !strings.Contains(strings.TrimPrefix(clean, "plan/items/"), "/"))
	if strings.HasPrefix(clean, "plan/items/") && strings.TrimSuffix(filepath.Base(clean), ".md") != value.ItemID {
		return fmt.Errorf("proposal item id must match its item path")
	}
	if value.Kind == "agent_b_addition" {
		if clean != "AGENT_B.md" || strings.Count(strings.TrimSuffix(value.NewText, "\n"), "\n") != strings.Count(strings.TrimSuffix(value.OldText, "\n"), "\n")+1 {
			return fmt.Errorf("AGENT_B.md proposals must add exactly one line")
		}
	} else if !planPath {
		return fmt.Errorf("proposal path is outside plan.md and plan/items")
	}
	if value.OldText == "" || value.OldText == value.NewText || len(value.OldText)+len(value.NewText) > 128*1024 {
		return fmt.Errorf("proposal must replace one non-empty exact span with different bounded text")
	}
	switch value.Kind {
	case "add":
		if !strings.Contains(value.NewText, value.OldText) || len(value.NewText) <= len(value.OldText) {
			return fmt.Errorf("add proposals must preserve the exact source span")
		}
	case "drop":
		if value.NewText != "" {
			return fmt.Errorf("drop proposals must replace the source span with empty text")
		}
	case "reorder":
		if !sameNonemptyLines(value.OldText, value.NewText) {
			return fmt.Errorf("reorder proposals must preserve the same non-empty lines")
		}
	}
	if len(value.SourceMessageIDs) > 32 {
		return fmt.Errorf("too many source messages")
	}
	return nil
}

func recordedPlanProposal(item *session.Session, proposal planProposal) bool {
	for _, message := range item.MessagesCopy() {
		for _, recorded := range message.PlanProposals {
			if reflect.DeepEqual(recorded, events.PlanProposal(proposal)) {
				return true
			}
		}
	}
	return false
}

func sameNonemptyLines(left, right string) bool {
	lines := func(value string) []string {
		result := []string{}
		for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
			if line != "" {
				result = append(result, line)
			}
		}
		sort.Strings(result)
		return result
	}
	return reflect.DeepEqual(lines(left), lines(right))
}

func (s *Server) acceptPlanProposal(r *http.Request, item *session.Session, proposal planProposal) (string, error) {
	planDir, err := s.planDirFor(item)
	if err != nil {
		return "", err
	}
	if s.runner == nil {
		return "", fmt.Errorf("plan writer is unavailable")
	}
	writer := item
	if item.Role != "d" || filepath.Clean(item.PlanDir) != filepath.Clean(planDir) {
		writer = &session.Session{
			ID: item.ID, Role: "d", Workspace: item.Workspace, PlanID: filepath.Base(planDir),
			PlanDir: planDir, PlanRepo: item.Workspace, PlansRoot: filepath.Dir(planDir),
			ToolsEnabled: map[string]bool{"edit_file": true}, LastSeen: map[string]time.Time{},
		}
	}
	writer.SetPlanPage(true)
	// The accepted old_text is the browser's exact observed span; count that
	// observation for the existing cross-session edit coordinator.
	writer.Touch(filepath.ToSlash(proposal.Path))
	outcome := s.runner.AcceptPlanEdit(r.Context(), writer, proposal.Path, proposal.OldText, proposal.NewText)
	if !outcome.OK {
		return "", fmt.Errorf("%s", strings.TrimPrefix(outcome.Content, "error: "))
	}
	s.runner.SettlePlanTurns(r.Context(), item, proposal.ItemID, proposal.SourceMessageIDs)
	s.runner.PublishBudget(r.Context(), item)
	return outcome.Content, nil
}

func (s *Server) planDirFor(item *session.Session) (string, error) {
	if item.Role == "d" && item.PlanDir != "" {
		return item.PlanDir, nil
	}
	plans, err := session.ListPlans(filepath.Join(s.roots.Data, "plans"))
	if err != nil {
		return "", err
	}
	for _, plan := range plans {
		if plan.Repo != "" && strings.EqualFold(filepath.Clean(plan.Repo), filepath.Clean(item.Workspace)) {
			return filepath.Join(s.roots.Data, "plans", plan.ID), nil
		}
	}
	return "", fmt.Errorf("this chat's folder has no plan")
}
