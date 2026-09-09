import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";

import { agentKey, lifetimeRows } from "./console-lifetime.js";

test("Console agent selector keys named objects", () => {
  assert.equal(agentKey({ name: "Home Coder" }), "home-coder");
});

test("Lifetime rows expose all six per-brief reliability fields", () => {
  const rows = new Map(lifetimeRows({ runs: 2, turns: 4, prompt_tokens: 100, completion_tokens: 20, cached_tokens: 50, worker_reliability: { briefs: 2, completed: 1, interventions: 2, reworked: 1, silent: 1, model_failures: 1, harness_failures: 2, brief_failures: 3 } }));
  for (const label of ["completion rate", "interventions / brief", "cost / brief", "failure attribution model | harness | brief", "rework rate", "silence rate"]) assert.ok(rows.has(label), label);
  assert.equal(rows.get("failure attribution model | harness | brief"), "1 | 2 | 3");
});

test("Console body owns selectors tools lifetime clear flush and conditional instruments", () => {
  const html = fs.readFileSync(new URL("../index.html", import.meta.url), "utf8");
  const script = fs.readFileSync(new URL("app.js", import.meta.url), "utf8");
  for (const value of ["console-agent", "console-tools", "console-stats", "clear-stats", "flush-memory", "console-live"]) assert.match(html, new RegExp(value));
  assert.doesNotMatch(html + script, /console-closed|renderClosed|deleteChat/);
  assert.match(script, /showLive = .*\["running", "queued", "paused", "stopping"\]/);
  assert.match(script, /patchEndedRun/);
  assert.match(script, /if \(next\) setActive\(next\.id\)/);
});
