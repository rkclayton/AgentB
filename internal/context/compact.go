// Package contextmgr batches compaction oldest-first. Editing the oldest prefix
// invalidates the model cache, so doing this rarely and in one batch minimizes
// repeated prefill work.
package contextmgr

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"harness/internal/events"
	"harness/internal/session"
)

type Counter func(string) (int, bool)
type Compactor struct{ bus *events.Bus }

func New(bus *events.Bus) *Compactor { return &Compactor{bus: bus} }

func (c *Compactor) Supersede(s *session.Session, runID string, turn int, count Counter) bool {
	messages := s.MessagesCopy()
	changed := false
	for current := range messages {
		item := messages[current]
		if item.Role != "tool" || item.Turn != turn || item.Elided || (item.Name != "read_file" && item.Name != "search_text") {
			continue
		}
		currentCall, ok := callFor(messages, item.ToolCallID)
		if !ok {
			continue
		}
		for prior := 0; prior < current; prior++ {
			older := messages[prior]
			if older.Role != "tool" || older.Name != item.Name || older.Elided {
				continue
			}
			olderCall, ok := callFor(messages, older.ToolCallID)
			if !ok || !supersedes(item.Name, olderCall.Arguments, currentCall.Arguments) {
				continue
			}
			messages[prior] = elide(older, olderCall.Arguments, count)
			c.updated(s, runID, messages[prior])
			changed = true
		}
	}
	if changed {
		s.ReplaceMessages(messages)
	}
	return changed
}

func (c *Compactor) ElideOld(s *session.Session, runID string, used, target int, count Counter) (bool, int) {
	messages := s.MessagesCopy()
	toolIndexes := []int{}
	for index, item := range messages {
		if item.Role == "tool" && !item.Elided {
			toolIndexes = append(toolIndexes, index)
		}
	}
	skip := map[int]bool{}
	for _, index := range toolIndexes[max(0, len(toolIndexes)-4):] {
		skip[index] = true
	}
	affected := []string{}
	before := used
	for index, item := range messages {
		if used <= target {
			break
		}
		if skip[index] || item.Elided || (item.Category != "files" && item.Category != "results" && item.Category != "fetched") {
			continue
		}
		call, _ := callFor(messages, item.ToolCallID)
		updated := elide(item, call.Arguments, count)
		used -= max(0, item.Tokens-updated.Tokens)
		messages[index] = updated
		affected = append(affected, item.ID)
		c.updated(s, runID, updated)
	}
	if len(affected) == 0 {
		return false, used
	}
	s.ReplaceMessages(messages)
	c.bus.Publish(events.New(events.Compaction, s.ID, runID, map[string]any{"kind": "elide", "before": before, "after": used, "affected_ids": affected}))
	return true, used
}

func (c *Compactor) Summarize(s *session.Session, runID string, summary events.Message) bool {
	messages := s.MessagesCopy()
	if len(messages) <= 7 {
		return false
	}
	keep := map[int]bool{0: true}
	for index := max(1, len(messages)-6); index < len(messages); index++ {
		keep[index] = true
	}
	affected := []string{}
	out := []events.Message{messages[0], summary}
	for index := 1; index < len(messages); index++ {
		if keep[index] {
			out = append(out, messages[index])
		} else {
			affected = append(affected, messages[index].ID)
		}
	}
	before, after := tokenSum(messages), tokenSum(out)
	s.ReplaceMessages(out)
	c.bus.Publish(events.New(events.Compaction, s.ID, runID, map[string]any{"kind": "summarize", "before": before, "after": after, "affected_ids": affected, "summary_message_id": summary.ID}))
	return true
}

func (c *Compactor) updated(s *session.Session, runID string, message events.Message) {
	c.bus.Publish(events.New(events.MessageUpdated, s.ID, runID, map[string]any{"id": message.ID, "patch": map[string]any{"content": message.Content, "tokens": message.Tokens, "elided": true}}))
}
func elide(message events.Message, rawArgs string, count Counter) events.Message {
	label := keyArgs(message.Name, rawArgs)
	stub := fmt.Sprintf("[elided: %s %s, %d tokens]", message.Name, label, message.Tokens)
	message.Content = stub
	message.Elided = true
	message.Tokens, message.Estimated = count(stub)
	return message
}
func callFor(messages []events.Message, id string) (events.ToolCall, bool) {
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.ID == id {
				return call, true
			}
		}
	}
	return events.ToolCall{}, false
}
func supersedes(name, older, current string) bool {
	if name == "search_text" {
		return canonical(older) == canonical(current)
	}
	var a, b struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if json.Unmarshal([]byte(older), &a) != nil || json.Unmarshal([]byte(current), &b) != nil || a.Path != b.Path {
		return false
	}
	if a.Offset == 0 { a.Offset = 1 }
	if b.Offset == 0 { b.Offset = 1 }
	if a.Limit == 0 { a.Limit = 200 }
	if b.Limit == 0 { b.Limit = 200 }
	return a.Offset <= b.Offset+b.Limit-1 && b.Offset <= a.Offset+a.Limit-1
}
func canonical(raw string) string {
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil { return raw }
	data, _ := json.Marshal(value)
	return string(data)
}
func keyArgs(name, raw string) string {
	var args map[string]any
	_ = json.Unmarshal([]byte(raw), &args)
	keys := make([]string, 0, len(args))
	for key := range args { keys = append(keys, key) }
	sort.Strings(keys)
	parts := []string{}
	for _, key := range keys { parts = append(parts, fmt.Sprintf("%s %v", key, args[key])) }
	if name == "read_file" {
		path, _ := args["path"].(string)
		offset := number(args["offset"], 1)
		limit := number(args["limit"], 200)
		return fmt.Sprintf("%s lines %d–%d", path, offset, offset+limit-1)
	}
	return strings.Join(parts, " ")
}
func number(value any, fallback int) int { if n, ok := value.(float64); ok { return int(n) }; return fallback }
func tokenSum(messages []events.Message) int { total := 0; for _, message := range messages { total += message.Tokens }; return total }
