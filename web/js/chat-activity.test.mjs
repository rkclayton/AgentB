import assert from "node:assert/strict";
import test from "node:test";

import { liveActivityText, showsStreamCaret } from "./chat-activity.js";

const running = (activity) => ({ run: { status: "running" }, activity });

test("live activity names model production and the executing tool without inventing elapsed time", () => {
  assert.equal(liveActivityText(running({ stage: "call_model", stage_state: "enter", stream: { has_chunk: true } })), "model producing");
  assert.equal(liveActivityText(running({ stage: "execute", stage_state: "enter", active_tool: "read_file" })), "tool executing · read_file");
  assert.equal(liveActivityText(running({ stage: "execute", stage_state: "enter" })), "tool executing · unknown");
});

test("live activity names the known wait after an exited stage without retaining that stage", () => {
  assert.equal(liveActivityText(running({ stage: "assemble", stage_state: "exit" })), "waiting for model");
  assert.equal(liveActivityText(running({ stage: "call_model", stage_state: "exit" })), "waiting for response parsing");
  assert.equal(liveActivityText(running({ stage: "parse", stage_state: "exit" })), "waiting for next action");
  assert.equal(liveActivityText(running({ stage: "dispatch", stage_state: "exit" })), "waiting for tool execution");
  assert.equal(liveActivityText(running({ stage: "execute", stage_state: "exit" })), "waiting for result recording");
  assert.equal(liveActivityText(running({ stage: "append", stage_state: "exit" })), "waiting for context check");
  assert.equal(liveActivityText(running({ stage: "compact", stage_state: "exit" })), "waiting for next turn");
  assert.equal(liveActivityText(running({ stage: "wait_user", stage_state: "exit" })), "waiting to resume");
  assert.equal(liveActivityText(running({ stage: "future_stage", stage_state: "enter" })), "waiting · state unknown");
  assert.equal(liveActivityText(running({ stage: "future_stage", stage_state: "exit" })), "waiting · state unknown");
  assert.equal(liveActivityText({ run: { status: "idle" }, activity: {} }), "");
});

test("stream caret exists only beside unfinished prose that has started arriving", () => {
  assert.equal(showsStreamCaret({ type: "agent", text: "part", done: false }), true);
  assert.equal(showsStreamCaret({ type: "agent", reasoning: "thinking", done: false }), false);
  assert.equal(showsStreamCaret({ type: "agent", text: "done", done: true }), false);
  assert.equal(showsStreamCaret({ type: "tool", text: "output", done: false }), false);
});
