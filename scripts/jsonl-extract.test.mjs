import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { extractRun, readJSONL, selectRun } from "./jsonl-extract.mjs";

const root = fs.mkdtempSync(path.join(os.tmpdir(), "agentb-jsonl-extract-"));
try {
  const tape = path.join(root, "tape.jsonl");
  const event = (seq, ts, type, data) => ({ seq, ts, session_id: "s1", run_id: "r1", type, data });
  fs.writeFileSync(tape, [
    event(1, "2026-09-15T00:00:00.000Z", "run.started", {}),
    event(2, "2026-09-15T00:00:01.000Z", "model.request", { turn: 1 }),
    event(3, "2026-09-15T00:00:02.000Z", "tool.call", { name: "read_file", arguments: '{"path":"logic/logic.go"}' }),
    event(4, "2026-09-15T00:00:03.000Z", "tool.result", { ok: true }),
    event(5, "2026-09-15T00:00:04.000Z", "tool.call", { call_id: "bad", name: "read_file", arguments: { path: "LOGIC\\logic.go" }, turn: 1 }),
    event(6, "2026-09-15T00:00:05.000Z", "tool.result", { call_id: "bad", name: "read_file", ok: false, preview: "error: path is outside workspace", turn: 1 }),
    event(7, "2026-09-15T00:00:05.500Z", "tool.call", { call_id: "fixed", name: "read_file", arguments: { path: "logic/logic.go" }, turn: 1 }),
    event(8, "2026-09-15T00:00:05.750Z", "tool.result", { call_id: "fixed", name: "read_file", ok: true, preview: "ok", turn: 1 }),
    event(9, "2026-09-15T00:00:06.000Z", "model.response", { usage: { prompt_tokens: 100, completion_tokens: 20, cached_tokens: 60 } }),
    event(10, "2026-09-15T00:00:07.000Z", "run.stopped", { reason: "done", detail: "complete", turns: 1 }),
    { seq: 11, ts: "2026-09-15T00:00:08.000Z", session_id: "other", run_id: "r2", type: "run.stopped", data: { reason: "done", turns: 9 } },
  ].map(JSON.stringify).join("\n") + "\n");
  const records = readJSONL([tape]);
  assert.equal(selectRun(records, { sessionID: "s1", runID: "r1" }).length, 10);
  assert.deepEqual(extractRun(records, { sessionID: "s1", runID: "r1" }), {
    session_id: "s1", run_id: "r1", completion: true, stop_reason: "done",
    stop: { reason: "done", detail: "complete", turn: 1 }, turns: 1,
    tool_calls: 3, tool_errors: 1, error_class_counts: { "outside jail": 1 },
    tool_error_details: [{
      call_id: "bad", tool: "read_file", turn: 1, class: "outside jail",
      arguments: { path: "LOGIC\\logic.go" }, preview: "error: path is outside workspace",
      protected_refusal: true, repeated_next: false, identical_next: false,
      recovered_next: true, next_tool: "read_file", next_call_id: "fixed",
    }],
    rereads: 2, elapsed_ms: 7000,
    prompt_tokens: 100, completion_tokens: 20, tokens: 120, cached_tokens: 60, cache_hit: 0.6, records: 10,
  });
  process.stdout.write("PASS shared JSONL extraction\n");
} finally {
  fs.rmSync(root, { recursive: true, force: true });
}
