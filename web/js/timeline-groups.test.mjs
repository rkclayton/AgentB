import assert from "node:assert/strict";
import test from "node:test";

import { groupAdjacentRuns, groupToolRuns, toolGroupRange, toolGroupStatus, toolResultText } from "./timeline-groups.js";

function model(seq, id, name, args) {
  return {
    kind: "model",
    event: {
      seq,
      type: "model.response",
      data: { tool_calls: [{ id, name, arguments: JSON.stringify(args) }] },
    },
  };
}

function compact(seq) {
  return { kind: "compaction", event: { seq, type: "compaction", data: {} } };
}

test("consecutive same-tool turns collapse without crossing another tool", () => {
  const entries = [
    model(1, "read-1", "read_file", { path: "a.css", offset: 1, limit: 3000 }),
    compact(2),
    model(3, "read-2", "read_file", { path: "a.css", offset: 3001, limit: 3000 }),
    compact(4),
    model(5, "shell-1", "shell", { command: "Get-Date" }),
    model(6, "read-3", "read_file", { path: "a.css", offset: 6001, limit: 3000 }),
  ];
  const grouped = groupToolRuns(entries);

  assert.equal(grouped.length, 4);
  assert.equal(grouped[0].kind, "tool-group");
  assert.equal(grouped[0].tool, "read_file");
  assert.equal(grouped[0].firstCallID, "read-1");
  assert.deepEqual(grouped[0].items.map((entry) => entry.event.seq), [1, 2, 3]);
  assert.equal(grouped[1].event.seq, 4);
  assert.equal(grouped[2].event.seq, 5);
  assert.equal(grouped[3].event.seq, 6);
});

test("shared adjacent scan retains bridges only when a matching member follows", () => {
  const entries = [{ kind: "call", name: "read" }, { kind: "bridge" }, { kind: "call", name: "read" }, { kind: "bridge" }, { kind: "call", name: "shell" }];
  const grouped = groupAdjacentRuns(entries, (entry) => entry?.kind === "call" ? { key: entry.name } : null, (entry) => entry?.kind === "bridge");
  assert.equal(grouped[0].kind, "adjacent-group");
  assert.equal(grouped[0].items.length, 3);
  assert.deepEqual(grouped.slice(1), entries.slice(3));
});

test("group range uses the varying argument and status exposes failures", () => {
  const group = groupToolRuns([
    model(1, "read-1", "read_file", { path: "a.css", offset: 1, limit: 3000 }),
    model(2, "read-2", "read_file", { path: "a.css", offset: 3001, limit: 3000 }),
    model(3, "read-3", "read_file", { path: "a.css", offset: 6001, limit: 3000 }),
  ])[0];
  const calls = new Map([
    ["read-1", { data: { args: { path: "a.css", offset: 1, limit: 3000 } } }],
    ["read-2", { data: { args: { path: "a.css", offset: 3001, limit: 3000 } } }],
    ["read-3", { data: { args: { path: "a.css", offset: 6001, limit: 3000 } } }],
  ]);
  const results = new Map([
    ["read-1", { data: { ok: true, ms: 4 } }],
    ["read-2", { data: { ok: false, ms: 7 } }],
    ["read-3", { data: { ok: true, ms: 5 } }],
  ]);

  assert.deepEqual(toolGroupRange(group, calls), {
    key: "offset", first: 1, last: 6001, numeric: true,
  });
  assert.deepEqual(toolGroupStatus(group, results), {
    ids: ["read-1", "read-2", "read-3"],
    failed: 1,
    pending: 0,
    untrusted: false,
    duration: 16,
  });
});

test("an elided message does not hide the recorded failure preview", () => {
  assert.equal(
    toolResultText(
      { elided: true, content: "[elided: read_file missing.css]" },
      { ok: false, preview: "error: missing.css was not found" },
    ),
    "error: missing.css was not found",
  );
  assert.equal(
    toolResultText(
      { elided: true, content: "[elided: read_file a.css]" },
      { ok: true, preview: "abbreviated" },
      "complete recorded result",
    ),
    "complete recorded result",
  );
});
