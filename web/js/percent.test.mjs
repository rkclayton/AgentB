import assert from "node:assert/strict";
import test from "node:test";
import { percentClass } from "./percent.js";

test("percentClass bounds and rounds values for CSP-safe sizing", () => {
  assert.equal(percentClass(-4), "pct-0");
  assert.equal(percentClass(12.49), "pct-50");
  assert.equal(percentClass(12.62), "pct-50");
  assert.equal(percentClass(140), "pct-400");
  assert.equal(percentClass(Number.NaN), "pct-0");
});
