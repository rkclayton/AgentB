import assert from "node:assert/strict";
import test from "node:test";

import { groupResponseRows, hasVisibleChatContent, isThinThought, responseBlocks, responseSummary, thinThoughtTokenLimit } from "./chat-response-groups.js";

const tool = (key, name, ok = true, ms = 1) => ({ type: "tool", key, name, args: {}, result: { ok, ms } });
const thought = (key, tokens, text = "") => ({ type: "agent", key, reasoning: "x", reasoningTokens: tokens, text, done: true, thinkingMS: 2 });

test("Chat ports adjacent grouping and absorbs only fixed-token thin thoughts", () => {
  assert.equal(isThinThought(thought("limit", thinThoughtTokenLimit)), true);
  assert.equal(isThinThought(thought("over", thinThoughtTokenLimit + 1)), false);
  const rows = groupResponseRows([
    tool("r1", "read_file"), thought("thin", 12), tool("r2", "read_file", false, 3),
    thought("long", 65), tool("r3", "read_file"), tool("s1", "shell"), tool("r4", "read_file"),
  ]);
  assert.deepEqual(rows.map((row) => row.kind || row.type), ["tool-group", "agent", "tool", "tool", "tool"]);
  assert.deepEqual(rows[0], {
    kind: "tool-group", key: "tool-group:r1", tool: "read_file", items: [tool("r1", "read_file"), thought("thin", 12), tool("r2", "read_file", false, 3)],
    calls: 2, thoughts: 1, failed: 1, duration: 6,
  });
});

test("collapsed turn arithmetic exposes failures and preserves every item", () => {
  const items = [thought("t1", 8), tool("a", "read_file", false, 7), { type: "notice", key: "n", event: { type: "run.aborted", data: {} } }, { type: "agent", key: "answer", text: "done", done: true }];
  assert.deepEqual(responseSummary(items), { tools: 1, thoughts: 1, answers: 1, failed: 1, duration: 9 });
  const expanded = groupResponseRows([tool("a", "read_file"), thought("t2", 4), tool("b", "read_file")])[0].items;
  assert.equal(expanded.length, 3);
  assert.deepEqual(expanded.map((item) => item.key), ["a", "t2", "b"]);
});

test("only an explicit failed tool result counts as a tool failure", () => {
  assert.equal(responseSummary([{ type: "tool", key: "bad", name: "read_file" }]).failed, 0);
  assert.equal(responseSummary([{ type: "notice", key: "bad-render", event: { type: "error", data: { where: "ui" } } }]).failed, 0);
  assert.equal(responseSummary([tool("failed", "read_file", false)]).failed, 1);
});

test("response blocks keep prose visible and assign only their following steps", () => {
  const items = [
    thought("leading", 8),
    { type: "agent", key: "first", text: "First prose", reasoning: "first thought", reasoningTokens: 3, done: true },
    tool("after-first", "read_file"),
    { type: "notice", key: "notice-first", event: { type: "compaction", data: {} } },
    { type: "agent", key: "second", text: "Second prose", reasoning: "second thought", reasoningTokens: 4, done: true },
    tool("after-second", "shell"),
  ];
  const blocks = responseBlocks(items);
  assert.deepEqual(blocks.map((block) => ({
    key: block.key,
    prose: block.prose?.text || "",
    steps: block.steps.map((item) => item.key),
  })), [
    { key: "response-block:leading:leading", prose: "", steps: ["leading"] },
    { key: "response-block:first", prose: "First prose", steps: ["thought:first", "after-first", "notice-first"] },
    { key: "response-block:second", prose: "Second prose", steps: ["thought:second", "after-second"] },
  ]);
  assert.equal(blocks[1].steps[0].text, "");
  assert.equal(blocks[1].steps[0].reasoning, "first thought");
});

test("tool-only responses create no empty prose region", () => {
  const blocks = responseBlocks([tool("only", "recall")]);
  assert.equal(blocks.length, 1);
  assert.equal(blocks[0].prose, null);
  assert.deepEqual(blocks[0].steps.map((item) => item.key), ["only"]);
});

test("only completed agent entries with no content are omitted from Chat", () => {
  assert.equal(hasVisibleChatContent({ type: "agent", key: "empty", done: true }), false);
  assert.equal(hasVisibleChatContent({ type: "agent", key: "reasoning", reasoning: "thinking", done: true }), true);
  assert.equal(hasVisibleChatContent({ type: "agent", key: "answer", text: "done", done: true }), true);
  assert.equal(hasVisibleChatContent({ type: "agent", key: "streaming", done: false }), true);
  assert.equal(hasVisibleChatContent({ type: "tool", key: "tool" }), true);
  assert.equal(hasVisibleChatContent({ type: "notice", key: "notice" }), true);
  assert.equal(hasVisibleChatContent({ type: "notice", key: "empty-delivery", event: { type: "files.delivered", data: { items: [] } } }), false);
  assert.equal(hasVisibleChatContent({ type: "notice", key: "delivery", event: { type: "files.delivered", data: { items: [{}] } } }), true);
  assert.equal(hasVisibleChatContent(null), true);
});
