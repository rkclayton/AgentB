import assert from "node:assert/strict";
import test from "node:test";

import { agentAuthor, chatRowText, closeConfirmText, openSessions, sessionTitle } from "./chat-lifecycle.js";

const idle = {
  id: "s2",
  agent_name: "Coder",
  main_profile: "Home API",
  created_at: "2026-09-07T12:00:00Z",
  closed: false,
  run: { status: "idle" },
  timeline: [
    { type: "run.started", run_id: "r1" },
    { type: "run.stopped", run_id: "r1" },
    { type: "run.started", run_id: "r2" },
  ],
  chat: [
    { type: "user", text: "Summarize the attached contract\nKeep headings." },
    { type: "tool", name: "write_file", args: { path: "notes.md" }, result: { ok: true } },
    { type: "tool", name: "edit_file", args: { path: "todo.md" }, result: { ok: true } },
    { type: "notice", event: { type: "memory.noted" } },
  ],
};

test("Chat row is relative time · first user line · runs · state glyph", () => {
  assert.equal(chatRowText(idle, Date.parse("2026-09-07T12:05:00Z")), "5m · Summarize the attached contract · 2 runs · ○");
});

test("Open chat list excludes durable closed sessions", () => {
  assert.deepEqual(openSessions({ s2: idle, s3: { ...idle, id: "s3", closed: true } }).map((session) => session.id), ["s2"]);
});

test("Agent and title labels use the persisted main-profile display name", () => {
  assert.equal(agentAuthor(idle), "agent_b · Coder");
  assert.equal(agentAuthor(idle, "aux"), "agent_c · Coder");
  assert.equal(sessionTitle(idle), "Coder");
});

test("Close confirmation counts changed files and memory entries without deleting either", () => {
  assert.equal(closeConfirmText(idle), "Close “Summarize the attached contract”?\n\nThis session changed 2 files and wrote 1 memory entry. Closing keeps the chat, logs, workspace files, memory, profile, and enabled tools.");
});
