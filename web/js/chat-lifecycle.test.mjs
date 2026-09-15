import assert from "node:assert/strict";
import test from "node:test";

import { agentAuthor, chatRowText, openSessions, sessionTitle } from "./chat-lifecycle.js";

const idle = {
  id: "s2",
  agent_name: "Coder",
  b_profile: "Home API",
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

test("Assistant labels use only role and the header carries only role and profile", () => {
  assert.equal(agentAuthor(idle), "agent_b");
  assert.equal(agentAuthor(idle, "c"), "agent_c");
  assert.equal(sessionTitle(idle), "agent_b · Home API");
  assert.equal(sessionTitle({ ...idle, role: "d", plan_name: "Release map", workspace: "C:\\code\\vesper" }), "agent_d · Home API");
});
