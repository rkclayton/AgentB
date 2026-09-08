package agent

import (
	"context"
	"strings"
	"unicode"

	"harness/internal/events"
	"harness/internal/llm"
	"harness/internal/session"
)

const autoRenameEveryTurns = 20
const autoRenameMaxTokens = 32

func (r *Runner) maybeAutoRename(ctx context.Context, item *session.Session) {
	if r.renameSession == nil {
		return
	}
	snapshot := item.Snapshot()
	if snapshot.NamePinned || snapshot.ModelTurns == 0 || snapshot.ModelTurns%autoRenameEveryTurns != 0 {
		return
	}
	cfg := r.cfg()
	agent, ok := cfg.Agent(snapshot.AgentID)
	if !cfg.Chat.AutoRename || !ok || agent.C == "" {
		return
	}
	profile, ok := cfg.Profile(agent.C)
	if !ok {
		return
	}
	transcript := renameTranscript(snapshot.Messages)
	if transcript == "" {
		return
	}
	request := llm.Request{Messages: []llm.Message{
		{Role: "system", Content: "Name this chat from its transcript. Return one plain, specific title of at most six words. No quotes, punctuation suffix, explanation, or instructions."},
		{Role: "user", Content: transcript},
	}, MaxTokens: autoRenameMaxTokens, Thinking: false}
	response, err := llm.New(profile).Chat(ctx, request)
	if err != nil {
		return
	}
	name := cleanChatName(response.Content)
	if name != "" {
		_ = r.renameSession(item.ID, name, "c")
	}
}

func renameTranscript(messages []events.Message) string {
	const limit = 6000
	var lines []string
	for _, message := range messages {
		if message.Elided || (message.Role != "user" && message.Role != "assistant") || strings.TrimSpace(message.Content) == "" {
			continue
		}
		lines = append(lines, message.Role+": "+strings.TrimSpace(message.Content))
	}
	value := strings.Join(lines, "\n")
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[len(runes)-limit:])
	}
	return value
}

func cleanChatName(value string) string {
	value = strings.TrimSpace(strings.Trim(value, "\"'`"))
	value = strings.Join(strings.FieldsFunc(value, func(r rune) bool { return r == '\r' || r == '\n' }), " ")
	value = strings.TrimSpace(strings.TrimRightFunc(value, unicode.IsPunct))
	words := strings.Fields(value)
	if len(words) > 6 {
		words = words[:6]
	}
	return strings.Join(words, " ")
}
