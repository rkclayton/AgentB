package operatorfiles

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
	workspaceinfo "harness/internal/workspace"
)

const (
	InboxMaxBytes  = 4 << 10
	InboxMaxLines  = 40
	OutboxMaxLines = 200
)

type Action struct {
	Stop     bool
	Revision string
	Delay    time.Duration
	Decision string
}

type Status struct {
	Root             string   `json:"root"`
	AttachmentsPath  string   `json:"attachments_path"`
	AttachmentFiles  int      `json:"attachment_files"`
	AttachmentBytes  int64    `json:"attachment_bytes"`
	InboxPath        string   `json:"inbox_path"`
	OutboxPath       string   `json:"outbox_path"`
	StatePath        string   `json:"state_path"`
	InstructionDir   string   `json:"instruction_dir,omitempty"`
	InstructionFile  string   `json:"instruction_file,omitempty"`
	InstructionFound []string `json:"instruction_found"`
}

type Manager struct {
	root, logDir string
	cfg          func() config.Config
	now          func() time.Time
	mu           sync.Mutex
	runStarted   map[string]time.Time
}

func New(root, logDir string, cfg func() config.Config) *Manager {
	return &Manager{root: filepath.Clean(root), logDir: filepath.Clean(logDir), cfg: cfg, now: time.Now, runStarted: map[string]time.Time{}}
}

func (m *Manager) Root() string            { return m.root }
func (m *Manager) InboxPath() string       { return filepath.Join(m.root, "INBOX.md") }
func (m *Manager) OutboxPath() string      { return filepath.Join(m.root, "OUTBOX.md") }
func (m *Manager) StatePath() string       { return filepath.Join(m.root, "STATE.md") }
func (m *Manager) AttachmentsPath() string { return filepath.Join(m.root, "attachments") }

func (m *Manager) Ensure() error {
	if err := os.MkdirAll(m.AttachmentsPath(), 0o700); err != nil {
		return fmt.Errorf("create operator attachments: %w", err)
	}
	for _, path := range []string{m.InboxPath(), m.OutboxPath()} {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) CheckInbox(sessionID string, approvalPending bool) (Action, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := os.ReadFile(m.InboxPath())
	if os.IsNotExist(err) {
		return Action{}, nil
	}
	if err != nil {
		return Action{}, err
	}
	if len(data) == 0 {
		return Action{}, nil
	}
	lines := splitLines(string(data))
	if len(data) > InboxMaxBytes || len(lines) > InboxMaxLines {
		_ = m.appendOutboxLocked(sessionID, "needs you: INBOX.md exceeds 40 lines / 4 KB and was left unread")
		return Action{}, fmt.Errorf("INBOX.md exceeds 40 lines / 4 KB")
	}
	first := strings.TrimSpace(lines[0])
	if first == "" {
		return Action{}, m.clearInboxLocked()
	}
	payload := strings.TrimSpace(strings.Join(lines[1:], "\n"))
	upper := strings.ToUpper(first)
	action := Action{}
	switch {
	case upper == "STOP":
		action.Stop = true
		_ = m.appendOutboxLocked(sessionID, "stopped: STOP read from INBOX.md")
	case strings.HasPrefix(upper, "REVISE:"):
		action.Revision = strings.TrimSpace(first[len("REVISE:"):])
		if payload != "" {
			action.Revision = strings.TrimSpace(action.Revision + "\n" + payload)
		}
		_ = m.appendOutboxLocked(sessionID, "pause: revision read from INBOX.md")
	case strings.HasPrefix(upper, "TASK:"):
		task := strings.TrimSpace(first[len("TASK:"):])
		if payload != "" {
			task = strings.TrimSpace(task + " " + strings.Join(strings.Fields(payload), " "))
		}
		if task == "" {
			return Action{}, fmt.Errorf("TASK requires text")
		}
		if err := appendLine(filepath.Join(m.root, "PLAN.md"), "- [ ] "+task); err != nil {
			return Action{}, err
		}
		_ = m.appendOutboxLocked(sessionID, "done: queued task in PLAN.md — "+oneLine(task))
	case strings.HasPrefix(upper, "DELAY:"):
		value := strings.TrimSpace(first[len("DELAY:"):])
		delay, parseErr := time.ParseDuration(value)
		if parseErr != nil || delay < 0 || delay > 24*time.Hour {
			return Action{}, fmt.Errorf("DELAY must be a duration from 0 through 24h")
		}
		action.Delay = delay
		_ = m.appendOutboxLocked(sessionID, "pause: delaying "+delay.String()+" from INBOX.md")
	case approvalPending && mailboxDecision(first) != "":
		if !m.cfg().OperatorFiles.AllowMailboxApprovals {
			_ = m.appendOutboxLocked(sessionID, "needs you: mailbox approval ignored because Allow approvals from the mailbox is off")
		} else {
			action.Decision = mailboxDecision(first)
			_ = m.appendOutboxLocked(sessionID, "done: approval answer read from INBOX.md")
		}
	default:
		note := first
		if payload != "" {
			note += " " + strings.Join(strings.Fields(payload), " ")
		}
		action.Revision = note
		_ = m.appendOutboxLocked(sessionID, "done: noted from INBOX.md — "+oneLine(note))
	}
	if err := m.clearInboxLocked(); err != nil {
		return Action{}, err
	}
	return action, nil
}

func mailboxDecision(value string) string {
	value = strings.TrimSpace(value)
	upper := strings.ToUpper(value)
	for _, prefix := range []string{"ANSWER:", "APPROVE:"} {
		if strings.HasPrefix(upper, prefix) {
			value = strings.TrimSpace(value[len(prefix):])
			break
		}
	}
	value = strings.ToLower(strings.ReplaceAll(value, " ", "_"))
	switch value {
	case "yes", "approve", "once":
		return "once"
	case "session", "chat", "for_this_chat":
		return "session"
	case "no", "deny":
		return "deny"
	case "continue", "stop":
		return value
	default:
		return ""
	}
}

func (m *Manager) clearInboxLocked() error {
	temporary, err := os.CreateTemp(m.root, ".INBOX-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := os.Rename(temporaryPath, m.InboxPath()); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}

func (m *Manager) AppendOutbox(sessionID, sentence string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.appendOutboxLocked(sessionID, sentence)
}

func (m *Manager) appendOutboxLocked(sessionID, sentence string) error {
	path := m.OutboxPath()
	count, err := lineCount(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if count >= OutboxMaxLines {
		stamp := m.now().UTC().Format("20060102T150405.000000000Z")
		if err := os.Rename(path, filepath.Join(m.root, "OUTBOX-"+stamp+".md")); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	line := fmt.Sprintf("%s | %s | %s", m.now().UTC().Format(time.RFC3339), valueOr(sessionID, "-"), oneLine(sentence))
	return appendLine(path, line)
}

func (m *Manager) HandleEvent(event events.Event, snapshot *session.Snapshot) {
	if event.SessionID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	switch event.Type {
	case events.RunStarted:
		m.runStarted[event.SessionID] = now
		_ = m.writeStateLocked(event, snapshot, "running", "")
	case events.ModelRequest:
		_ = m.writeStateLocked(event, snapshot, "running", "")
	case events.ApprovalRequired:
		_ = m.appendOutboxLocked(event.SessionID, "needs you: approval is waiting in "+labelOf(snapshot, event.SessionID))
		_ = m.writeStateLocked(event, snapshot, "waiting for you", "approval")
	case events.ModelUnreachable:
		_ = m.appendOutboxLocked(event.SessionID, "pause: model is unreachable for "+labelOf(snapshot, event.SessionID))
		_ = m.writeStateLocked(event, snapshot, "paused", "model")
	case events.RunStopped:
		reason := eventString(event.Data, "reason")
		kind := "stopped"
		if reason == "done" {
			kind = "done"
		}
		_ = m.appendOutboxLocked(event.SessionID, kind+": "+labelOf(snapshot, event.SessionID)+" ("+valueOr(reason, "unknown")+")")
		_ = m.writeStateLocked(event, snapshot, "idle", "")
		delete(m.runStarted, event.SessionID)
	case events.ChatExported:
		_ = m.appendOutboxLocked(event.SessionID, "ready to test: closed chat exported to "+eventString(event.Data, "path"))
	}
}

func (m *Manager) ReadyToTest(sessionID, detail string) error {
	return m.AppendOutbox(sessionID, "ready to test: "+oneLine(detail))
}

func (m *Manager) writeStateLocked(event events.Event, snapshot *session.Snapshot, state, waiting string) error {
	started := m.runStarted[event.SessionID]
	elapsed := time.Duration(0)
	if !started.IsZero() {
		elapsed = m.now().Sub(started).Round(time.Second)
	}
	turn := 0
	if snapshot != nil {
		turn = snapshot.Run.Turn
	}
	content := fmt.Sprintf("# Agent_b state\nupdated: %s\nstatus: %s\nchat: %s\nitem: turn %d\nelapsed: %s\nwaiting: %s\n", m.now().UTC().Format(time.RFC3339), state, labelOf(snapshot, event.SessionID), turn, elapsed, valueOr(waiting, "nothing"))
	temporary, err := os.CreateTemp(m.root, ".STATE-*.tmp")
	if err != nil {
		return err
	}
	path := temporary.Name()
	if _, err := temporary.WriteString(content); err != nil {
		_ = temporary.Close()
		_ = os.Remove(path)
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		_ = os.Remove(path)
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	if err := os.Rename(path, m.StatePath()); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func (m *Manager) Status(dir string) (Status, error) {
	result := Status{Root: m.root, AttachmentsPath: m.AttachmentsPath(), InboxPath: m.InboxPath(), OutboxPath: m.OutboxPath(), StatePath: m.StatePath(), InstructionDir: dir, InstructionFound: []string{}}
	entries, err := os.ReadDir(m.AttachmentsPath())
	if err != nil && !os.IsNotExist(err) {
		return result, err
	}
	for _, entry := range entries {
		if info, infoErr := entry.Info(); infoErr == nil && info.Mode().IsRegular() {
			result.AttachmentFiles++
			result.AttachmentBytes += info.Size()
		}
	}
	if strings.TrimSpace(dir) != "" {
		abs, _, canonicalErr := workspaceinfo.Canonical(dir)
		if canonicalErr != nil {
			return result, canonicalErr
		}
		result.InstructionDir = abs
		for _, name := range []string{"AGENT_B.md", "AGENTS.md", "CLAUDE.md"} {
			if regular(filepath.Join(abs, name)) {
				result.InstructionFound = append(result.InstructionFound, name)
			}
		}
		if len(result.InstructionFound) > 0 {
			result.InstructionFile = result.InstructionFound[0]
		}
	}
	return result, nil
}

func (m *Manager) EmptyAttachments() (int, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, err := os.ReadDir(m.AttachmentsPath())
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	files, bytes := 0, int64(0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(m.AttachmentsPath(), entry.Name())
		info, infoErr := os.Stat(path)
		if infoErr != nil || !info.Mode().IsRegular() {
			continue
		}
		if err := os.Remove(path); err != nil {
			return files, bytes, err
		}
		files++
		bytes += info.Size()
	}
	return files, bytes, nil
}

func (m *Manager) Adopt(dir string, cleanup bool) (string, []string, error) {
	abs, _, err := workspaceinfo.Canonical(dir)
	if err != nil {
		return "", nil, err
	}
	destination := filepath.Join(abs, "AGENT_B.md")
	if regular(destination) {
		return "", nil, fmt.Errorf("AGENT_B.md already exists; existing files were left untouched")
	}
	sources := []string{}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		path := filepath.Join(abs, name)
		if regular(path) {
			sources = append(sources, path)
		}
	}
	if len(sources) == 0 {
		return "", nil, fmt.Errorf("no AGENTS.md or CLAUDE.md to adopt")
	}
	parts := []string{}
	for _, source := range sources {
		data, readErr := os.ReadFile(source)
		if readErr != nil {
			return "", nil, readErr
		}
		if len(sources) == 1 {
			parts = append(parts, string(data))
		} else {
			parts = append(parts, "## From "+filepath.Base(source)+"\n\n"+strings.TrimSpace(string(data)))
		}
	}
	content := strings.Join(parts, "\n\n")
	if len(sources) > 1 {
		content = "# Repository instructions\n\n" + content + "\n"
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", nil, err
	}
	_, writeErr := file.WriteString(content)
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(destination)
		return "", nil, writeErr
	}
	removed := []string{}
	if cleanup {
		for _, source := range sources {
			if err := os.Remove(source); err != nil {
				return destination, removed, err
			}
			removed = append(removed, filepath.Base(source))
		}
	}
	return destination, removed, nil
}

func (m *Manager) ExportChat(snapshot session.Snapshot) (string, error) {
	dir := filepath.Join(m.root, "chats", DirKey(snapshot.Workspace))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	date := m.now().Format("2006-01-02")
	name := safeName(snapshot.Label)
	if name == "" {
		name = safeName(snapshot.ID)
	}
	base := filepath.Join(dir, date+"-"+name+".md")
	path := availablePath(base)
	var body strings.Builder
	fmt.Fprintf(&body, "# %s\n\n- chat: `%s`\n- agent: %s\n- workspace: `%s`\n- closed: %s\n\n## Transcript\n", snapshot.Label, snapshot.ID, snapshot.AgentName, snapshot.Workspace, m.now().UTC().Format(time.RFC3339))
	messages, err := m.exportMessages(snapshot)
	if err != nil {
		return "", err
	}
	for _, message := range messages {
		if message.Category == "summary" {
			fmt.Fprintf(&body, "\n### Summary\n\n%s\n", summaryExportContent(message.Content))
			continue
		}
		switch message.Role {
		case "tool":
			status := "ok"
			if message.OK != nil && !*message.OK {
				status = "failed"
			}
			fmt.Fprintf(&body, "\n- tool `%s` · %s · turn %d\n", valueOr(message.Name, "unknown"), status, message.Turn)
		default:
			heading := map[string]string{"user": "You", "assistant": "Agent", "system": "System"}[message.Role]
			if heading == "" {
				heading = message.Role
			}
			fmt.Fprintf(&body, "\n### %s\n\n%s\n", heading, message.Content)
			for _, attachment := range message.Attachments {
				fmt.Fprintf(&body, "\n- attachment: `%s`\n", attachment.Path)
			}
			for _, call := range message.ToolCalls {
				fmt.Fprintf(&body, "\n- tool `%s` · requested · turn %d\n", call.Name, message.Turn)
			}
		}
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func summaryExportContent(content string) string {
	content = strings.TrimPrefix(content, "Progress note (auto-summary of earlier turns):\n")
	if index := strings.Index(content, "\n\n[BEGIN COMPACTION EVIDENCE]"); index >= 0 {
		content = content[:index]
	}
	return strings.TrimSpace(content)
}

// exportMessages reads the current JSONL generation when available so close
// exports retain turns that context compaction removed from the live prompt.
func (m *Manager) exportMessages(snapshot session.Snapshot) ([]events.Message, error) {
	if strings.TrimSpace(snapshot.LogPath) == "" {
		return snapshot.Messages, nil
	}
	logPath, err := filepath.Abs(snapshot.LogPath)
	if err != nil {
		return nil, err
	}
	logRoot, err := filepath.Abs(m.logDir)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(logRoot, logPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("session log is outside the configured log directory")
	}
	file, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	messages := []events.Message{}
	decoder := json.NewDecoder(file)
	for {
		var event events.Event
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("read chat export log: %w", err)
		}
		if event.SessionID != snapshot.ID {
			continue
		}
		switch event.Type {
		case events.MessageAppended:
			var wrapper struct {
				Message events.Message `json:"message"`
			}
			data, marshalErr := json.Marshal(event.Data)
			if marshalErr != nil {
				return nil, marshalErr
			}
			if err := json.Unmarshal(data, &wrapper); err != nil {
				return nil, err
			}
			messages = append(messages, wrapper.Message)
		case events.MessageRemoved:
			var removed struct {
				ID string `json:"id"`
			}
			data, _ := json.Marshal(event.Data)
			_ = json.Unmarshal(data, &removed)
			kept := messages[:0]
			for _, message := range messages {
				if message.ID != removed.ID {
					kept = append(kept, message)
				}
			}
			messages = kept
		}
	}
	return messages, nil
}

func DirKey(dir string) string {
	abs, key, err := workspaceinfo.Canonical(dir)
	if err != nil {
		abs, key = filepath.Clean(dir), filepath.ToSlash(filepath.Clean(dir))
	}
	sum := sha256.Sum256([]byte(key))
	name := safeName(filepath.Base(abs))
	if name == "" {
		name = "workspace"
	}
	return name + "-" + hex.EncodeToString(sum[:4])
}

func (m *Manager) ApplyRetention() ([]string, error) {
	days := m.cfg().OperatorFiles.LogRetentionDays
	if days <= 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(m.logDir)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	cutoff := m.now().Add(-time.Duration(days) * 24 * time.Hour)
	removed := []string{}
	for _, entry := range entries {
		if entry.IsDir() || strings.EqualFold(entry.Name(), "evidence") || !strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		path := filepath.Join(m.logDir, entry.Name())
		if err := os.Remove(path); err != nil {
			return removed, err
		}
		removed = append(removed, path)
	}
	sort.Strings(removed)
	return removed, nil
}

func (m *Manager) RunRetention(ctx context.Context) {
	_, _ = m.ApplyRetention()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = m.ApplyRetention()
		}
	}
}

func splitLines(value string) []string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.TrimSuffix(value, "\n")
	if value == "" {
		return []string{}
	}
	return strings.Split(value, "\n")
}

func lineCount(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		count++
	}
	return count, scanner.Err()
}

func appendLine(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(strings.TrimRight(line, "\r\n") + "\n")
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	return writeErr
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func safeName(value string) string {
	value = strings.Trim(unsafeName.ReplaceAllString(strings.TrimSpace(value), "-"), ".-_ ")
	if len(value) > 80 {
		value = value[:80]
	}
	return strings.ToLower(value)
}

func availablePath(path string) string {
	extension := filepath.Ext(path)
	stem := strings.TrimSuffix(path, extension)
	for index := 1; ; index++ {
		candidate := path
		if index > 1 {
			candidate = stem + "-" + strconv.Itoa(index) + extension
		}
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

func oneLine(value string) string { return strings.Join(strings.Fields(value), " ") }
func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
func labelOf(snapshot *session.Snapshot, fallback string) string {
	if snapshot != nil && snapshot.Label != "" {
		return snapshot.Label
	}
	return fallback
}
func eventString(data any, key string) string {
	if values, ok := data.(map[string]any); ok {
		if value, ok := values[key].(string); ok {
			return value
		}
	}
	return ""
}
func regular(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
