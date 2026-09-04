import assert from "node:assert/strict";
import test from "node:test";

import {
  createOperatorStatusController,
  isOperatorStateEvent,
  renderOperatorStatus,
} from "./operator-status.js";

class FakeButton {
  constructor() {
    this.attributes = new Map();
    this.dataset = {};
    this.disabled = false;
    this.image = { src: "", srcset: "" };
    this.listeners = new Map();
  }

  querySelector(selector) {
    return selector === "img" ? this.image : null;
  }

  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }

  addEventListener(type, listener) {
    this.listeners.set(type, listener);
  }

  click() {
    return this.listeners.get("click")();
  }
}

test("operator indicator renders both server-observed states", () => {
  for (const button of [new FakeButton(), new FakeButton()]) {
    renderOperatorStatus(button, { operator_context: false });
    assert.equal(button.image.src, "/static/assets/operator-off-24.png");
    assert.equal(button.attributes.get("aria-pressed"), "false");

    renderOperatorStatus(button, { operator_context: true });
    assert.equal(button.image.src, "/static/assets/operator-on-24.png");
    assert.equal(button.attributes.get("aria-pressed"), "true");
  }
});

test("only authoritative operator-state events trigger immediate rendering", () => {
  for (const type of ["snapshot", "shell.identity", "operator.context"]) {
    assert.equal(isOperatorStateEvent({ type }), true, type);
  }
  for (const type of ["init", "config.changed", "model.delta"]) {
    assert.equal(isOperatorStateEvent({ type }), false, type);
  }
});

test("enable confirms once and waits for observed state before changing the image", async () => {
  const button = new FakeButton();
  let identity = { operator_context: false };
  let confirmations = 0;
  const requests = [];
  let finishRequest;
  const request = new Promise((resolve) => { finishRequest = resolve; });
  const controller = createOperatorStatusController(button, {
    identity: () => identity,
    confirmEnable: () => { confirmations++; return true; },
    setOperatorContext: (enabled) => { requests.push(enabled); return request; },
    reportError: assert.fail,
  });

  const toggled = button.click();
  assert.equal(confirmations, 1);
  assert.deepEqual(requests, [true]);
  assert.equal(button.disabled, true);
  assert.equal(button.dataset.pending, "true");
  assert.equal(button.image.src, "/static/assets/operator-off-24.png");
  finishRequest({});
  await toggled;
  assert.equal(button.image.src, "/static/assets/operator-off-24.png");

  identity = { operator_context: true };
  controller.render();
  assert.equal(button.image.src, "/static/assets/operator-on-24.png");
});

test("failed enable stays off and reports the endpoint error", async () => {
  const button = new FakeButton();
  const errors = [];
  createOperatorStatusController(button, {
    identity: () => ({ operator_context: false }),
    confirmEnable: () => true,
    setOperatorContext: async () => { throw new Error("grant refused"); },
    reportError: (message) => errors.push(message),
  });

  await button.click();
  assert.equal(button.image.src, "/static/assets/operator-off-24.png");
  assert.deepEqual(errors, ["grant refused"]);
  assert.equal(button.disabled, false);
});

test("disable is immediate and unconfirmed, then expiry renders off", async () => {
  const button = new FakeButton();
  let identity = { operator_context: true };
  let confirmations = 0;
  const requests = [];
  const controller = createOperatorStatusController(button, {
    identity: () => identity,
    confirmEnable: () => { confirmations++; return true; },
    setOperatorContext: async (enabled) => requests.push(enabled),
    reportError: assert.fail,
  });

  await button.click();
  assert.equal(confirmations, 0);
  assert.deepEqual(requests, [false]);

  identity = { operator_context: false };
  controller.render();
  assert.equal(button.image.src, "/static/assets/operator-off-24.png");
  assert.equal(button.attributes.get("aria-pressed"), "false");
});
