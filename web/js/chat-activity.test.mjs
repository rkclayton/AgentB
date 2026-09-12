import assert from "node:assert/strict";
import test from "node:test";

import { liveActivityText, showsStreamCaret } from "./chat-activity.js";

const running = (activity) => ({ run: { status: "running" }, activity });

test("live activity names model production and the executing tool without inventing elapsed time", () => {
  assert.equal(liveActivityText(running({ stage: "call_model", stage_state: "enter", stream: { has_chunk: true } })), "model producing");
  assert.equal(liveActivityText(running({ stage: "execute", stage_state: "enter", active_tool: "read_file" })), "tool executing · read_file");
  assert.equal(liveActivityText(running({ stage: "execute", stage_state: "enter" })), "tool executing · unknown");
});

test("live activity does not repeat an exited or unrecognized stage", () => {
  assert.equal(liveActivityText(running({ stage: "call_model", stage_state: "exit" })), "waiting · state unknown");
  assert.equal(liveActivityText(running({ stage: "future_stage", stage_state: "enter" })), "waiting · state unknown");
  assert.equal(liveActivityText({ run: { status: "idle" }, activity: {} }), "");
});

test("stream caret exists only beside unfinished prose that has started arriving", () => {
  assert.equal(showsStreamCaret({ type: "agent", text: "part", done: false }), true);
  assert.equal(showsStreamCaret({ type: "agent", reasoning: "thinking", done: false }), false);
  assert.equal(showsStreamCaret({ type: "agent", text: "done", done: true }), false);
  assert.equal(showsStreamCaret({ type: "tool", text: "output", done: false }), false);
});
