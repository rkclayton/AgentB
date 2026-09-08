import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";

import { agentKey, closedChats, closedRow, deletePrompt, lifetimeRows } from "./console-lifetime.js";

test("Console agent selector keys named objects and closed rows stay dense", () => {
  assert.equal(agentKey({ name: "Home Coder" }), "home-coder");
  const session = { id: "s2", agent_id: "home-coder", closed: true, created_at: "2026-09-07T12:00:00Z", run: { status: "idle" }, chat: [{ type: "user", text: "Fix the build" }], timeline: [] };
  assert.deepEqual(closedChats({ s2: session, s3: { ...session, id: "s3", closed: false } }, "home-coder").map((item) => item.id), ["s2"]);
  assert.match(closedRow(session), / · Fix the build · 0 runs · ○ · ×$/);
});

test("Delete confirmation names event file and optional memory counts", () => {
  const session = { chat: [{ type: "user", text: "Remove me" }] };
  const text = deletePrompt(session, { events: 12, jsonl_files: 2, memory_writes: [{ note: "x" }] });
  for (const value of ["12 events", "2 JSONL files", "1 memory entries", "Workspace and exchange files are kept"]) assert.match(text, new RegExp(value));
});

test("Lifetime rows expose all six per-brief reliability fields", () => {
  const rows = new Map(lifetimeRows({ runs: 2, turns: 4, prompt_tokens: 100, completion_tokens: 20, cached_tokens: 50, worker_reliability: { briefs: 2, completed: 1, interventions: 2, reworked: 1, silent: 1, model_failures: 1, harness_failures: 2, brief_failures: 3 } }));
  for (const label of ["completion rate", "interventions / brief", "cost / brief", "failure attribution model | harness | brief", "rework rate", "silence rate"]) assert.ok(rows.has(label), label);
  assert.equal(rows.get("failure attribution model | harness | brief"), "1 | 2 | 3");
});

test("Console body owns selectors tools lifetime closed delete clear flush and conditional instruments", () => {
  const html = fs.readFileSync(new URL("../index.html", import.meta.url), "utf8");
  const script = fs.readFileSync(new URL("app.js", import.meta.url), "utf8");
  for (const value of ["console-agent", "console-tools", "console-stats", "console-closed", "clear-stats", "flush-memory", "console-live"]) assert.match(html, new RegExp(value));
  assert.match(script, /showLive = .*\["running", "queued", "paused", "stopping"\]/);
  assert.match(script, /patchEndedRun/);
  assert.match(script, /if \(next\) setActive\(next\.id\)/);
  assert.match(script, /drop_memory: dropMemory/);
});
