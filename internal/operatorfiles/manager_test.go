package operatorfiles

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
)

func testManager(t *testing.T) (*Manager, *config.Config) {
	t.Helper()
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	cfg := config.Defaults(filepath.Join(root, "workspace"))
	manager := New(root, logDir, func() config.Config { return cfg })
	if err := manager.Ensure(); err != nil {
		t.Fatal(err)
	}
	return manager, &cfg
}

func writeInbox(t *testing.T, manager *Manager, value string) {
	t.Helper()
	if err := os.WriteFile(manager.InboxPath(), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInboxVerbsTaskDelayAndNote(t *testing.T) {
	manager, _ := testManager(t)
	writeInbox(t, manager, "REVISE: use the smaller patch\nand keep tests\n")
	action, err := manager.CheckInbox("s1", false)
	if err != nil || action.Revision != "use the smaller patch\nand keep tests" {
		t.Fatalf("revision=%q err=%v", action.Revision, err)
	}
	writeInbox(t, manager, "TASK: add the export golden\n")
	if _, err := manager.CheckInbox("s1", false); err != nil {
		t.Fatal(err)
	}
	plan, err := os.ReadFile(filepath.Join(manager.Root(), "PLAN.md"))
	if err != nil || string(plan) != "- [ ] add the export golden\n" {
		t.Fatalf("plan=%q err=%v", plan, err)
	}
	writeInbox(t, manager, "DELAY: 250ms\n")
	action, err = manager.CheckInbox("s1", false)
	if err != nil || action.Delay != 250*time.Millisecond {
		t.Fatalf("delay=%s err=%v", action.Delay, err)
	}
	writeInbox(t, manager, "Remember the release screenshot\n")
	action, err = manager.CheckInbox("s1", false)
	if err != nil || action.Revision != "Remember the release screenshot" {
		t.Fatalf("note=%q err=%v", action.Revision, err)
	}
	writeInbox(t, manager, "ANSWER: operator_mode\n")
	if action, err := manager.CheckInbox("s1", true); err != nil || action.Decision != "" {
		t.Fatalf("retired decision=%q err=%v", action.Decision, err)
	}
	writeInbox(t, manager, "ANSWER: run\n")
	if action, err := manager.CheckInbox("s1", true); err != nil || action.Decision != "" {
		t.Fatalf("hidden run decision=%q err=%v", action.Decision, err)
	}
	writeInbox(t, manager, "STOP\n")
	action, err = manager.CheckInbox("s1", false)
	if err != nil || !action.Stop {
		t.Fatalf("stop=%t err=%v", action.Stop, err)
	}
	cleared, _ := os.ReadFile(manager.InboxPath())
	if len(cleared) != 0 {
		t.Fatalf("INBOX was not cleared: %q", cleared)
	}
}

func TestInboxCapabilityTierAndCap(t *testing.T) {
	manager, cfg := testManager(t)
	writeInbox(t, manager, "ANSWER: session\n")
	action, err := manager.CheckInbox("s1", true)
	if err != nil || action.Decision != "" {
		t.Fatalf("off decision=%q err=%v", action.Decision, err)
	}
	cfg.OperatorFiles.AllowMailboxApprovals = true
	writeInbox(t, manager, "ANSWER: for this chat\n")
	action, err = manager.CheckInbox("s1", true)
	if err != nil || action.Decision != "session" {
		t.Fatalf("on decision=%q err=%v", action.Decision, err)
	}
	oversized := strings.Repeat("x", InboxMaxBytes+1)
	writeInbox(t, manager, oversized)
	if _, err := manager.CheckInbox("s1", false); err == nil {
		t.Fatal("oversized INBOX was accepted")
	}
	retained, _ := os.ReadFile(manager.InboxPath())
	if string(retained) != oversized {
		t.Fatal("oversized INBOX was changed")
	}
}

func TestOutboxRotatesAtTwoHundredLines(t *testing.T) {
	manager, _ := testManager(t)
	manager.now = func() time.Time { return time.Date(2026, 9, 8, 1, 2, 3, 4, time.UTC) }
	for index := 0; index < OutboxMaxLines+1; index++ {
		if err := manager.AppendOutbox("s1", "done: item"); err != nil {
			t.Fatal(err)
		}
	}
	if count, err := lineCount(manager.OutboxPath()); err != nil || count != 1 {
		t.Fatalf("active lines=%d err=%v", count, err)
	}
	matches, _ := filepath.Glob(filepath.Join(manager.Root(), "OUTBOX-*.md"))
	if len(matches) != 1 {
		t.Fatalf("rotations=%v", matches)
	}
	if count, err := lineCount(matches[0]); err != nil || count != OutboxMaxLines {
		t.Fatalf("rotated lines=%d err=%v", count, err)
	}
}

func TestOutboxUsesHumanEventVocabulary(t *testing.T) {
	manager, _ := testManager(t)
	snapshot := &session.Snapshot{ID: "s1", Label: "Release check"}
	manager.HandleEvent(events.New(events.ModelUnreachable, "s1", "r1", map[string]any{}), snapshot)
	manager.HandleEvent(events.New(events.ApprovalRequired, "s1", "r1", map[string]any{}), snapshot)
	manager.HandleEvent(events.New(events.RunStopped, "s1", "r1", map[string]any{"reason": "done"}), snapshot)
	manager.HandleEvent(events.New(events.RunStopped, "s1", "r2", map[string]any{"reason": "safe"}), snapshot)
	manager.HandleEvent(events.New(events.ChatExported, "s1", "", map[string]any{"path": "chats/release.md"}), snapshot)
	data, err := os.ReadFile(manager.OutboxPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pause:", "needs you:", "done:", "stopped:", "ready to test:"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("OUTBOX missing %q:\n%s", want, data)
		}
	}
}

func TestStateIsAtomicallyReplacedAtRunAndTurnBoundaries(t *testing.T) {
	manager, _ := testManager(t)
	now := time.Date(2026, 9, 8, 1, 2, 3, 0, time.UTC)
	manager.now = func() time.Time { return now }
	snapshot := &session.Snapshot{ID: "s1", Label: "Fix tests", Run: session.RunState{Turn: 2}}
	manager.HandleEvent(events.New(events.RunStarted, "s1", "r1", map[string]any{}), snapshot)
	now = now.Add(3 * time.Second)
	manager.HandleEvent(events.New(events.ModelRequest, "s1", "r1", map[string]any{"turn": 2}), snapshot)
	data, err := os.ReadFile(manager.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"status: running", "chat: Fix tests", "item: turn 2", "elapsed: 3s", "waiting: nothing"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("STATE missing %q:\n%s", want, data)
		}
	}
	if temporary, _ := filepath.Glob(filepath.Join(manager.Root(), ".STATE-*.tmp")); len(temporary) != 0 {
		t.Fatalf("temporary files remain: %v", temporary)
	}
}

func TestChatExportGoldenCollapsesToolsAndReferencesAttachments(t *testing.T) {
	manager, _ := testManager(t)
	manager.now = func() time.Time { return time.Date(2026, 9, 8, 2, 3, 4, 0, time.UTC) }
	ok := true
	path, err := manager.ExportChat(session.Snapshot{ID: "s1", Label: "Review build", AgentName: "Local", Workspace: filepath.Join(manager.Root(), "repo"), Messages: []events.Message{
		{Role: "user", Content: "Review this.", Attachments: []events.Attachment{{Path: "attachments/spec.pdf"}}},
		{Role: "system", Category: "summary", Content: "Progress note (auto-summary of earlier turns):\nKept the requested change.\n\n[BEGIN COMPACTION EVIDENCE]\n{\"excerpt\":\"prompt only\"}\n[END COMPACTION EVIDENCE]"},
		{Role: "assistant", Content: "Checking.", Turn: 1, ToolCalls: []events.ToolCall{{Name: "read_file"}}},
		{Role: "tool", Name: "read_file", Turn: 1, OK: &ok, Content: "body intentionally omitted"},
		{Role: "assistant", Content: "Done.", Turn: 1},
	}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	want := "# Review build\n\n- chat: `s1`\n- agent: Local\n- workspace: `" + filepath.Join(manager.Root(), "repo") + "`\n- closed: 2026-09-08T02:03:04Z\n\n## Transcript\n\n### You\n\nReview this.\n\n- attachment: `attachments/spec.pdf`\n\n### Summary\n\nKept the requested change.\n\n### Agent\n\nChecking.\n\n- tool `read_file` · requested · turn 1\n\n- tool `read_file` · ok · turn 1\n\n### Agent\n\nDone.\n"
	if string(data) != want {
		t.Fatalf("export mismatch\n--- got ---\n%s\n--- want ---\n%s", data, want)
	}
}

func TestChatExportReadsCurrentJSONLGenerationBeforeCompaction(t *testing.T) {
	manager, _ := testManager(t)
	logPath := filepath.Join(manager.logDir, "s1-current.jsonl")
	ok := true
	event := events.New(events.MessageAppended, "s1", "r1", map[string]any{"message": events.Message{ID: "tool-1", Role: "tool", Name: "shell", Turn: 1, OK: &ok}})
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(manager.logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := manager.ExportChat(session.Snapshot{ID: "s1", Label: "Compacted", AgentName: "Local", Workspace: manager.Root(), LogPath: logPath})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "- tool `shell` · ok · turn 1") {
		t.Fatalf("export = %q", body)
	}
}

func TestAdoptIsNonDestructiveByDefaultAndCleanupIsExplicit(t *testing.T) {
	manager, _ := testManager(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agent rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("claude rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, removed, err := manager.Adopt(dir, false)
	if err != nil || len(removed) != 0 {
		t.Fatalf("path=%q removed=%v err=%v", path, removed, err)
	}
	for _, name := range []string{"AGENT_B.md", "AGENTS.md", "CLAUDE.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
	}
	dir = t.TempDir()
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+" rules\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, removed, err = manager.Adopt(dir, true)
	if err != nil || len(removed) != 2 {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
}

func TestRetentionDeletesOnlyExpiredTopLevelJSONL(t *testing.T) {
	manager, cfg := testManager(t)
	cfg.OperatorFiles.LogRetentionDays = 30
	if err := os.MkdirAll(manager.logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 3, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	old := filepath.Join(manager.logDir, "old.jsonl")
	newer := filepath.Join(manager.logDir, "new.jsonl")
	evidence := filepath.Join(manager.logDir, "evidence", "old.jsonl")
	for _, path := range []string{old, newer, evidence} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.Chtimes(old, now.Add(-31*24*time.Hour), now.Add(-31*24*time.Hour))
	_ = os.Chtimes(evidence, now.Add(-31*24*time.Hour), now.Add(-31*24*time.Hour))
	removed, err := manager.ApplyRetention()
	if err != nil || len(removed) != 1 || removed[0] != old {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
	for _, path := range []string{newer, evidence} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("retained file %s: %v", path, err)
		}
	}
}
