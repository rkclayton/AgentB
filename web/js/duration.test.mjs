import assert from "node:assert/strict";
import test from "node:test";

import { formatDuration } from "./duration.js";

test("Tool rows share compact millisecond and second duration formatting", () => {
  assert.equal(formatDuration(327), "327 ms");
  assert.equal(formatDuration(5167), "5.2 s");
  assert.equal(formatDuration(null), "");
});
