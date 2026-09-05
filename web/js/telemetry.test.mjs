import assert from "node:assert/strict";
import test from "node:test";

import { applyStreamEvent, hydrateTelemetry, liveTelemetry, recordedTelemetry } from "./telemetry.js";

test("live telemetry projects chunk age, reasoning, and a recent rate", () => {
  const session = { timeline: [] };
  applyStreamEvent(session, { type: "model.request", run_id: "r1", data: { turn: 2 } }, 1000);
  applyStreamEvent(session, { type: "model.delta", run_id: "r1", data: { turn: 2, kind: "reasoning", text: "123456789" } }, 2000);
  applyStreamEvent(session, { type: "model.delta", run_id: "r1", data: { turn: 2, kind: "content", text: "123456789" } }, 3000);
  assert.deepEqual(liveTelemetry(session, 4000), { ageSeconds: 1, reasoningTokens: 3, rate: 2, hasChunk: true });
});

test("live telemetry counts Unicode characters rather than UTF-16 units", () => {
  const session = { timeline: [] };
  applyStreamEvent(session, { type: "model.request", run_id: "r1", data: { turn: 1 } }, 1000);
  applyStreamEvent(session, { type: "model.delta", run_id: "r1", data: { turn: 1, kind: "reasoning", text: "🙂🙂🙂🙂" } }, 2000);
  assert.equal(liveTelemetry(session, 3000).reasoningTokens, 2);
});

test("snapshot hydration reconstructs an active stream", () => {
  const session = { timeline: [
    { type: "model.request", ts: "2026-01-01T00:00:01Z", run_id: "r2", data: { turn: 1 } },
    { type: "model.delta", ts: "2026-01-01T00:00:02Z", run_id: "r2", data: { turn: 1, kind: "reasoning", text: "abcdefgh" } },
  ] };
  hydrateTelemetry(session);
  assert.equal(session._streamTelemetry.reasoningChars, 8);
  assert.equal(session._streamTelemetry.done, false);
});

test("replay telemetry is explicitly final recorded data", () => {
  const session = { timeline: [{
    type: "model.response",
    data: { reasoning_tokens: 1847, reasoning_tokens_estimated: true, timings: { predicted_per_second: 23.6 } },
  }] };
  assert.deepEqual(recordedTelemetry(session), { reasoningTokens: 1847, reasoningTokensEstimated: true, rate: 24 });
});
