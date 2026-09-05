import assert from "node:assert/strict";
import test from "node:test";

globalThis.document = {
  hidden: false,
  getElementById: () => null,
  addEventListener: () => {},
};
globalThis.window = { addEventListener: () => {} };
globalThis.EventSource = class {
  addEventListener() {}
};

const { reduce, store } = await import("./bus.js");

test("browser reducer preserves authoritative aggregates through events and reset", () => {
  reduce({
    type: "snapshot",
    data: {
      sessions: { main: { id: "main", run: { status: "idle", last_stop_reason: "" }, tools: [], messages: [], timeline: [], model_turns: 4, compaction_count: 2, compaction_token_delta: -90, compaction_model_calls: 1, compaction_prompt_tokens: 80, compaction_completion_tokens: 20 } },
      replay: false,
      servers: [],
      config: {},
    },
  });
  assert.equal(store.sessions.main.model_turns, 4);
  reduce({ type: "model.response", session_id: "main", run_id: "run", data: { turn: 5 } });
  reduce({ type: "compaction.summary", session_id: "main", run_id: "run", data: { dispatched: true, usage: { prompt_tokens: 40, completion_tokens: 10 } } });
  reduce({ type: "compaction", session_id: "main", run_id: "run", data: { before: 100, after: 75 } });
  reduce({ type: "run.stopped", session_id: "main", run_id: "run", data: { reason: "model_error" } });
  assert.equal(store.sessions.main.model_turns, 5);
  assert.equal(store.sessions.main.compaction_count, 3);
  assert.equal(store.sessions.main.compaction_token_delta, -115);
  assert.equal(store.sessions.main.compaction_model_calls, 2);
  assert.equal(store.sessions.main.compaction_prompt_tokens, 120);
  assert.equal(store.sessions.main.compaction_completion_tokens, 30);
  assert.equal(store.sessions.main.run.last_stop_reason, "model_error");
  reduce({ type: "session.reset", session_id: "main", data: {} });
  assert.equal(store.sessions.main.model_turns, 0);
  assert.equal(store.sessions.main.compaction_count, 0);
  assert.equal(store.sessions.main.compaction_token_delta, 0);
  assert.equal(store.sessions.main.compaction_model_calls, 0);
  assert.equal(store.sessions.main.compaction_prompt_tokens, 0);
  assert.equal(store.sessions.main.compaction_completion_tokens, 0);
  assert.equal(store.sessions.main.run.last_stop_reason, "");
});

test("browser reducer applies budget tool costs to session tools", () => {
  reduce({
    type: "snapshot",
    data: {
      sessions: { main: { id: "main", run: { status: "idle" }, tools: [{ name: "read_file", schema_tokens: 0, marginal_tokens: 0 }], messages: [], timeline: [] } },
      replay: false,
      servers: [],
      config: {},
    },
  });
  reduce({
    type: "budget",
    session_id: "main",
    data: { categories: { tools: 100 }, tool_schema_tokens: { read_file: 60 }, tool_marginal_tokens: { read_file: 20 } },
  });
  assert.equal(store.sessions.main.budget.categories.tools, 100);
  assert.equal(store.sessions.main.tools[0].schema_tokens, 60);
  assert.equal(store.sessions.main.tools[0].marginal_tokens, 20);
});
