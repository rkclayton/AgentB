package agent

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"harness/internal/config"
	"harness/internal/session"
)

type PromptRenderer struct {
	mu         sync.RWMutex
	path, text string
}

func LoadTemplate(path string) (*PromptRenderer, error) {
	r := &PromptRenderer{path: path}
	if err := r.Reload(); err != nil {
		return nil, err
	}
	return r, nil
}
func (r *PromptRenderer) Reload() error {
	data, err := os.ReadFile(r.path)
	if err != nil {
		return fmt.Errorf("system prompt %s: %w", r.path, err)
	}
	r.mu.Lock()
	r.text = string(data)
	r.mu.Unlock()
	return nil
}
func (r *PromptRenderer) Render(profile *config.Profile, s *session.Session, toolNames []string, memory string) string {
	return r.RenderMemoryParts(profile, s, toolNames, s.ProjectBlock, memory, s.AgentMemoryBlock)
}
func (r *PromptRenderer) RenderParts(profile *config.Profile, s *session.Session, toolNames []string, project, memory string) string {
	return r.RenderMemoryParts(profile, s, toolNames, project, memory, "")
}
func (r *PromptRenderer) RenderMemoryParts(profile *config.Profile, s *session.Session, toolNames []string, project, workspaceMemory, agentMemory string) string {
	r.mu.RLock()
	template := r.text
	r.mu.RUnlock()
	if profile.SystemPromptOverride != "" {
		template = profile.SystemPromptOverride
	}
	agentBlock := ""
	if addendum := strings.TrimSpace(s.PromptAddendum); addendum != "" {
		agentBlock = "BEGIN OPERATOR AGENT ADDENDUM\n" + addendum + "\nEND OPERATOR AGENT ADDENDUM"
	}
	if !strings.Contains(template, "{{agent}}") && agentBlock != "" {
		if strings.Contains(template, "{{project}}") {
			template = strings.Replace(template, "{{project}}", "{{agent}}\n{{project}}", 1)
		} else {
			template = strings.TrimRight(template, "\r\n") + "\n{{agent}}\n"
		}
	}
	value := strings.ReplaceAll(template, "{{workspace}}", s.Workspace)
	value = strings.ReplaceAll(value, "{{tools}}", strings.Join(toolNames, ", "))
	value = strings.ReplaceAll(value, "{{agent}}", agentBlock)
	value = strings.ReplaceAll(value, "{{project}}", project)
	memory := strings.TrimSpace(strings.Join([]string{strings.TrimSpace(workspaceMemory), strings.TrimSpace(agentMemory)}, "\n\n"))
	value = strings.ReplaceAll(value, "{{memory}}", memory)
	value = strings.ReplaceAll(value, "{{os_context}}", operatingSystemContext())
	value = strings.ReplaceAll(value, "{{date}}", time.Now().Format("2006-01-02"))
	if memory == "" && project == "" {
		value = strings.TrimRight(value, "\r\n")
	}
	return value
}
