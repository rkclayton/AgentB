import assert from "node:assert/strict";
import test from "node:test";

import { createPanelState } from "./panel-state.js";

function memoryStorage() {
  const values = new Map();
  return {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, String(value)),
  };
}

for (const scope of ["timeline", "state"]) {
  test(`${scope} expansion survives event renders and refresh`, () => {
    const storage = memoryStorage();
    const live = createPanelState(scope, storage);
    live.view("main").toggle("row-1");
    for (let event = 0; event < 50; event++)
      assert.equal(live.view("main").expanded.has("row-1"), true);

    const refreshed = createPanelState(scope, storage);
    assert.equal(refreshed.view("main").expanded.has("row-1"), true);
  });
}
