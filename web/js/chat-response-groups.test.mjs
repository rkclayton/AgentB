import assert from "node:assert/strict";
import test from "node:test";

import { groupResponseRows, isThinThought, responseSummary, thinThoughtTokenLimit } from "./chat-response-groups.js";

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
  assert.deepEqual(responseSummary(items), { tools: 1, thoughts: 1, answers: 1, failed: 2, duration: 9 });
  const expanded = groupResponseRows([tool("a", "read_file"), thought("t2", 4), tool("b", "read_file")])[0].items;
  assert.equal(expanded.length, 3);
  assert.deepEqual(expanded.map((item) => item.key), ["a", "t2", "b"]);
});

test("malformed tool rows count as failures before expansion", () => {
  assert.equal(responseSummary([{ type: "tool", key: "bad", name: "read_file" }]).failed, 1);
});
