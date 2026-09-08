import assert from "node:assert/strict";
import test from "node:test";

import { contextOccupancy } from "./context-occupancy.js";

test("occupancy groups model categories and accounts for the whole context", () => {
  const view = contextOccupancy({
    n_ctx: 1000,
    reserve: 200,
    categories: {
      system: 100,
      project: 25,
      tools: 150,
      files: 40,
      results: 30,
      fetched: 20,
      history: 80,
      summary: 10,
      workspace_memory: 20,
      agent_memory: 10,
    },
  });

  assert.deepEqual(
    view.items.map(({ key, value }) => [key, value]),
    [
      ["system", 100],
      ["project", 25],
      ["schemas", 150],
      ["tool-results", 90],
      ["history", 90],
      ["workspace-memory", 20],
      ["agent-memory", 10],
      ["free", 315],
      ["reserve", 200],
    ],
  );
  assert.equal(view.occupied, 485);
  assert.equal(view.items.reduce((sum, item) => sum + item.value, 0), 1000);
});

test("estimated source categories mark their group and derived free space", () => {
  const partial = contextOccupancy({
    n_ctx: 100,
    reserve: 10,
    estimated: true,
    estimated_categories: ["tools", "fetched"],
    categories: { system: 10, tools: 20, fetched: 5, history: 5 },
  });
  assert.deepEqual(
    partial.items.filter((item) => item.estimated).map((item) => item.key),
    ["schemas", "tool-results", "free"],
  );
  assert.equal(partial.estimated, true);

  const whollyEstimated = contextOccupancy({
    n_ctx: 100,
    estimated: true,
    categories: { system: 10 },
  });
  assert.deepEqual(
    whollyEstimated.items.filter((item) => item.estimated).map((item) => item.key),
    ["system", "project", "schemas", "tool-results", "history", "workspace-memory", "agent-memory", "free"],
  );
});

test("project and both memory layers remain separate occupancy categories", () => {
  const view = contextOccupancy({ n_ctx: 100, categories: { system: 10, project: 7, memory: 3 } });
  assert.deepEqual(view.items.slice(0, 3).map(({key,value}) => [key,value]), [["system",10],["project",7],["schemas",0]]);
  assert.deepEqual(view.items.slice(5, 7).map(({key,value}) => [key,value]), [["workspace-memory",3],["agent-memory",0]]);
});
