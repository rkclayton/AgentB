package agent

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"

	"harness/internal/session"
)

var addPlanPattern = regexp.MustCompile(`(?i)^\s*add\s+(.+?)\s+as\s+a\s+plan[.!]?\s*$`)

func requestedPlanPath(text string) (string, bool) {
	match := addPlanPattern.FindStringSubmatch(text)
	if len(match) != 2 {
		return "", false
	}
	value := strings.TrimSpace(match[1])
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	if value == "" || !filepath.IsAbs(filepath.FromSlash(value)) {
		return "", false
	}
	return filepath.Clean(filepath.FromSlash(value)), true
}

func (r *Runner) handlePlanRegistration(ctx context.Context, item *session.Session, runID string) (bool, string) {
	messages := item.MessagesCopy()
	if len(messages) == 0 || messages[len(messages)-1].Role != "user" {
		return false, ""
	}
	path, requested := requestedPlanPath(messages[len(messages)-1].Content)
	if !requested {
		return false, ""
	}
	if item.RegisterPlan == nil {
		return true, "plan registration is unavailable"
	}
	callID := r.id("plan-registration")
	approved, err := r.gate.WaitPolicyRequired(ctx, item, runID, callID, "plan registration", map[string]any{
		"path": path,
	})
	if err != nil {
		return true, "plan registration approval ended: " + err.Error()
	}
	if !approved {
		return true, "plan registration declined"
	}
	plan, created, err := item.RegisterPlan(path)
	if err != nil {
		return true, "plan registration failed: " + err.Error()
	}
	if !created {
		return true, "plan already exists: " + plan.Name
	}
	return true, "plan registered: " + plan.Name
}
