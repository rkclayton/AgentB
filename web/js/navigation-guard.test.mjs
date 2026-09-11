import assert from "node:assert/strict";
import test from "node:test";
import { createNavigationGuard } from "./navigation-guard.js";

function runtime(overrides = {}) {
  const calls = [];
  const sequence = { value: 0 };
  let pageshow;
  return {
    calls,
    firePageShow(persisted) { pageshow?.({ persisted }); },
    value: {
      enabled: true,
      begin: (details) => { calls.push(["begin", details]); return `nav-${++sequence.value}`; },
      suppress: (details) => calls.push(["suppress", details]),
      decorate: (target, navigationID) => `${target}${target.includes("?") ? "&" : "?"}navigation_id=${navigationID}${overrides.enabled === false ? "" : "&navigation_guard=1"}`,
      assign: (target) => { calls.push(["assign", target]); },
      onPageShow: (listener) => { pageshow = listener; },
      ...overrides,
    },
  };
}

test("guard accepts one request and suppresses later requests in the document", () => {
  const fake = runtime();
  const guard = createNavigationGuard(fake.value);
  assert.equal(guard.request({ kind: "flip" }, "/chat?session=main"), true);
  assert.equal(guard.request({ kind: "flip" }, "/?session=main"), false);
  assert.deepEqual(fake.calls.map(([kind]) => kind), ["begin", "assign", "suppress"]);
  assert.equal(fake.calls[1][1], "/chat?session=main&navigation_id=nav-1&navigation_guard=1");
});

test("guard is inert when the experiment is not enabled", () => {
  const fake = runtime({ enabled: false });
  const guard = createNavigationGuard(fake.value);
  assert.equal(guard.request({ kind: "flip" }, "/chat"), true);
  assert.equal(guard.request({ kind: "flip" }, "/"), true);
  assert.deepEqual(fake.calls.map(([kind]) => kind), ["begin", "assign", "begin", "assign"]);
  assert.equal(fake.calls[1][1], "/chat?navigation_id=nav-1");
  assert.equal(fake.calls[3][1], "/?navigation_id=nav-2");
});

test("guard resets after a back-forward cache restore", () => {
  const fake = runtime();
  const guard = createNavigationGuard(fake.value);
  assert.equal(guard.request({ kind: "flip" }, "/chat"), true);
  fake.firePageShow(true);
  assert.equal(guard.request({ kind: "flip" }, "/"), true);
  assert.deepEqual(fake.calls.map(([kind]) => kind), ["begin", "assign", "begin", "assign"]);
});

test("guard resets after thrown and explicit synchronous navigation failure", () => {
  let attempts = 0;
  const fake = runtime({
    assign: (target) => {
      fake.calls.push(["assign", target]);
      attempts++;
      if (attempts === 1) throw new Error("rejected");
      if (attempts === 2) return false;
      return true;
    },
  });
  const guard = createNavigationGuard(fake.value);
  assert.throws(() => guard.request({ kind: "flip" }, "/chat"), /rejected/);
  assert.equal(guard.request({ kind: "flip" }, "/chat"), false);
  assert.equal(guard.request({ kind: "flip" }, "/chat"), true);
  assert.deepEqual(fake.calls.map(([kind]) => kind), ["begin", "assign", "begin", "assign", "begin", "assign"]);
});
