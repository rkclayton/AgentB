import assert from "node:assert/strict";
import test from "node:test";

import { projectStopState, renderStopState } from "./stop-state.js";

class FakeButton {
  constructor() { this.disabled = false; this.dataset = {}; this.attributes = new Map(); this.className = "stop-sign"; }
  get classList() { return { toggle: (name, on) => { this.className = on ? `stop-sign ${name}` : "stop-sign"; } }; }
  setAttribute(name, value) { this.attributes.set(name, value); }
}

test("Stop projection is red only for an active live run after snapshot refresh", () => {
  const button = new FakeButton();
  renderStopState(button, { run: { status: "idle" } }, false);
  assert.deepEqual([button.dataset.state, button.disabled], ["idle", true]);
  renderStopState(button, { run: { status: "running" } }, false);
  assert.deepEqual([button.dataset.state, button.disabled], ["active", false]);
  renderStopState(button, { run: { status: "stopping" } }, false);
  assert.deepEqual([button.dataset.state, button.disabled, button.attributes.get("title")], ["stopping", false, "Emergency stop — cancel immediately"]);
});

test("Stop projection remains grey and disabled in replay", () => {
  assert.deepEqual(projectStopState({ run: { status: "running" } }, true), {
    active: false, disabled: true, state: "idle", label: "No active run to stop",
  });
});
