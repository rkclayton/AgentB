import assert from "node:assert/strict";
import test from "node:test";
import { aggregateRuns, markdown } from "./progress-replay.mjs";

test("replay summary separates passed and failed fires", () => {
  const runs = [
    { tape: "a.jsonl", pass: true, stop_reason: "done", turns: 2, tool_errors: 0, detectors: [{ detector: "novel_action", available: true, would_fire: false, values: {} }] },
    { tape: "b.jsonl", pass: false, stop_reason: "tool_errors", turns: 4, tool_errors: 3, detectors: [{ detector: "novel_action", available: true, would_fire: true, values: { first_fire_turn: 3 } }] },
  ];
  const report = aggregateRuns(runs);
  assert.deepEqual(report.detector_table[0], { detector: "novel_action", available: 2, passed_fires: 0, failed_fires: 1, total_fires: 1, implied_precision: 1 });
  const text = markdown(report);
  assert.match(text, /\| novel_action \| 2 \| 0 \| 1 \| 1 \| 100\.0% \|/);
  assert.match(text, /novel_action@3/);
});
