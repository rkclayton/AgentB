import assert from "node:assert/strict";
import test from "node:test";

import {
  createThinkingRenderer,
  hydrateAgentEntries,
  modelTurnKey,
  trimTimeline,
} from "./reasoning.js";

class FakeElement {
  constructor(tagName) {
    this.tagName = tagName;
    this.children = [];
    this.attributes = new Map();
    this.className = "";
    this.hidden = false;
    this.textContent = "";
  }

  append(...children) {
    this.children.push(...children);
  }

  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }
}

const fakeDocument = { createElement: (tagName) => new FakeElement(tagName) };

test("active model turn remains intact beyond the ordinary timeline tail", () => {
  const request = { type: "model.request", run_id: "r1", data: { turn: 1 } };
  const timeline = [request];
  for (let index = 0; index < 600; index++)
    timeline.push({ type: "model.delta", run_id: "r1", data: { turn: 1, kind: "reasoning", text: "x" } });

  assert.equal(modelTurnKey(request), "r1:1");
  assert.equal(trimTimeline(timeline, "r1:1").length, 601);
  assert.deepEqual(trimTimeline(timeline, ""), [request]);
});

test("completed streams cannot displace operational history", () => {
  const compaction = { type: "compaction", run_id: "r1", data: { before: 100, after: 60 } };
  const timeline = [compaction];
  for (let turn = 1; turn <= 40; turn++) {
    timeline.push({ type: "model.request", run_id: "r1", data: { turn } });
    for (let index = 0; index < 20; index++)
      timeline.push({ type: "model.delta", run_id: "r1", data: { turn, kind: "reasoning", text: "x" } });
    timeline.push({ type: "model.response", run_id: "r1", data: { turn } });
  }
  const trimmed = trimTimeline(timeline, "");
  assert.equal(trimmed.includes(compaction), true);
  assert.equal(trimmed.some((event) => event.type === "model.delta"), false);
  assert.equal(trimmed.filter((event) => event.type === "model.response").length, 40);
});

test("assistant message turn keys use the nested message turn", () => {
  assert.equal(modelTurnKey({ run_id: "r2", data: { message: { turn: 3 } } }), "r2:3");
});

test("stored assistant messages hydrate reasoning missing from the event tail", () => {
  const final = { type: "agent", done: true, text: "Final answer", reasoning: "", toolCallIDs: [] };
  const toolTurn = { type: "agent", done: true, text: "", reasoning: "", toolCallIDs: ["call-1"] };
  const messages = [
    { role: "assistant", content: "Final answer", reasoning: "full final reasoning" },
    { role: "assistant", content: "", reasoning: "full tool reasoning", tool_calls: [{ id: "call-1" }] },
  ];

  const matched = hydrateAgentEntries([final, toolTurn], messages);
  assert.equal(final.reasoning, "full final reasoning");
  assert.equal(toolTurn.reasoning, "full tool reasoning");
  assert.equal(matched.size, 2);
});

test("thinking renderer preserves its DOM and never opens to an empty body", () => {
  const expanded = new Set(["turn:r1:1"]);
  const renderer = createThinkingRenderer({
    document: fakeDocument,
    expanded,
    rerender: () => {},
    format: String,
    formatDuration: () => "1.2",
  });
  const active = { key: "turn:r1:1", reasoning: "", done: false, thinkingMS: null };

  renderer.begin();
  const first = renderer.render(active, 0);
  renderer.end();
  const firstDots = first.children[0].children[1].children[0];
  assert.equal(first.children[1].textContent, "Waiting for reasoning text…");

  renderer.begin();
  const second = renderer.render({ ...active, reasoning: "streamed thought" }, 4);
  renderer.end();
  assert.equal(second, first);
  assert.equal(second.children[0].children[1].children[0], firstDots);
  assert.equal(second.children[1].textContent, "streamed thought");

  renderer.begin();
  const completed = renderer.render({ ...active, reasoning: "streamed thought", done: true, thinkingMS: 1200 }, 4);
  renderer.end();
  assert.equal(completed, first);
  assert.equal(completed.children[0].children[2].textContent, "Thought 1.2 seconds (4 tokens)");

  renderer.begin();
  const unavailable = renderer.render({ ...active, done: true, thinkingMS: 1200 }, 4);
  renderer.end();
  assert.equal(unavailable.children[1].textContent, "Reasoning text is unavailable in this recording.");
});
