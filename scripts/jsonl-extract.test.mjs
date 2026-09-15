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
    event(5, "2026-09-15T00:00:04.000Z", "tool.call", { name: "read_file", arguments: { path: "LOGIC\\logic.go" } }),
    event(6, "2026-09-15T00:00:05.000Z", "tool.result", { ok: false }),
    event(7, "2026-09-15T00:00:06.000Z", "model.response", { usage: { prompt_tokens: 100, completion_tokens: 20, cached_tokens: 60 } }),
    event(8, "2026-09-15T00:00:07.000Z", "run.stopped", { reason: "done", turns: 1 }),
    { seq: 9, ts: "2026-09-15T00:00:08.000Z", session_id: "other", run_id: "r2", type: "run.stopped", data: { reason: "done", turns: 9 } },
  ].map(JSON.stringify).join("\n") + "\n");
  const records = readJSONL([tape]);
  assert.equal(selectRun(records, { sessionID: "s1", runID: "r1" }).length, 8);
  assert.deepEqual(extractRun(records, { sessionID: "s1", runID: "r1" }), {
    session_id: "s1", run_id: "r1", completion: true, stop_reason: "done", turns: 1,
    tool_calls: 2, tool_errors: 1, rereads: 1, elapsed_ms: 7000,
    prompt_tokens: 100, completion_tokens: 20, tokens: 120, cached_tokens: 60, cache_hit: 0.6, records: 8,
  });
  process.stdout.write("PASS shared JSONL extraction\n");
} finally {
  fs.rmSync(root, { recursive: true, force: true });
}
