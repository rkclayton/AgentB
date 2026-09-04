import assert from "node:assert/strict";
import test from "node:test";

import { createOperatorReconciler } from "./operator-reconcile.js";

function deferred() {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
}

test("reconnect reconciles a missed disable", async () => {
  let identity = { operator_context: true };
  const reconciler = createOperatorReconciler({
    readState: async () => ({ shell_identity: { operator_context: false } }),
    applyIdentity: (value) => { identity = value; },
  });

  assert.equal(await reconciler.reconcile(), true);
  assert.equal(identity.operator_context, false);
});

test("reconnect reconciles a missed enable", async () => {
  let identity = { operator_context: false };
  const reconciler = createOperatorReconciler({
    readState: async () => ({ shell_identity: { operator_context: true } }),
    applyIdentity: (value) => { identity = value; },
  });

  assert.equal(await reconciler.reconcile(), true);
  assert.equal(identity.operator_context, true);
});

test("an operator event supersedes an older reconciliation response", async () => {
  let identity = { operator_context: false };
  const pending = deferred();
  const reconciler = createOperatorReconciler({
    readState: () => pending.promise,
    applyIdentity: (value) => { identity = value; },
  });

  const reconciliation = reconciler.reconcile();
  identity = { operator_context: true };
  reconciler.observed();
  pending.resolve({ shell_identity: { operator_context: false } });

  assert.equal(await reconciliation, false);
  assert.equal(identity.operator_context, true);
});

test("simultaneous clients converge independently", async () => {
  let serverState = true;
  const identities = [false, false];
  const clients = identities.map((_, index) => createOperatorReconciler({
    readState: async () => ({ shell_identity: { operator_context: serverState } }),
    applyIdentity: (value) => { identities[index] = value.operator_context; },
  }));

  await Promise.all(clients.map((client) => client.reconcile()));
  assert.deepEqual(identities, [true, true]);
  serverState = false;
  await Promise.all(clients.map((client) => client.reconcile()));
  assert.deepEqual(identities, [false, false]);
});
