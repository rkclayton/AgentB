import assert from "node:assert/strict";
import test from "node:test";
import { agentTabLayout } from "./agent-tabs.js";

test("one Agent uses the strip without overflow", () => {
  assert.deepEqual(agentTabLayout(1, 240), { visible: 1, hidden: 0 });
});

test("three Agents shrink to the minimum before overflow", () => {
  assert.deepEqual(agentTabLayout(3, 220), { visible: 3, hidden: 0 });
});

test("twelve Agents collapse extras into the end menu", () => {
  assert.deepEqual(agentTabLayout(12, 220), { visible: 2, hidden: 10 });
});
