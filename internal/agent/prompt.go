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
	r.mu.RLock()
	template := r.text
	r.mu.RUnlock()
	if profile.SystemPromptOverride != "" {
		template = profile.SystemPromptOverride
	}
	value := strings.ReplaceAll(template, "{{workspace}}", s.Workspace)
	value = strings.ReplaceAll(value, "{{tools}}", strings.Join(toolNames, ", "))
	value = strings.ReplaceAll(value, "{{memory}}", memory)
	value = strings.ReplaceAll(value, "{{os_context}}", operatingSystemContext())
	value = strings.ReplaceAll(value, "{{date}}", time.Now().Format("2006-01-02"))
	if memory == "" {
		value = strings.TrimRight(value, "\r\n")
	}
	return value
}
