import assert from "node:assert/strict";
import test from "node:test";

globalThis.document = { hidden: false, getElementById: () => null, addEventListener: () => {} };
globalThis.window = { addEventListener: () => {} };
const stored = new Map();
globalThis.sessionStorage = { getItem: (key) => stored.get(key) || null, setItem: (key, value) => stored.set(key, value) };
globalThis.EventSource = class { addEventListener() {} };
const { reduce, setSelection, store, subscribe } = await import("./bus.js");

function snapshot(session = {}) {
  reduce({ type: "snapshot", data: { sessions: { main: { id: "main", cursor: { generation: "g", offset: 10 }, run: { status: "idle" }, tools: [], messages: [], timeline: [], ...session } }, replay: false, servers: [], config: {} } });
}
function patch(offset, operations, previous = offset - 1) {
  reduce({ type: "projection.patch", data: { schema_version: 1, session_id: "main", previous_cursor: { generation: "g", offset: previous }, cursor: { generation: "g", offset }, operations } });
}

test("browser applies authoritative replace and append operations", () => {
  snapshot({ model_turns: 4 });
  patch(11, [{ op: "replace", path: "/model_turns", value: 5 }], 10);
  patch(12, [{ op: "append", path: "/timeline", value: { type: "model.response", data: { turn: 5 } } }], 11);
  assert.equal(store.sessions.main.model_turns, 5);
  assert.equal(store.sessions.main.timeline.length, 1);
  assert.equal(store.sessions.main.cursor.offset, 12);
});

test("append initializes an omitted empty projected string", () => {
  snapshot({
    run: { status: "running" },
    chat: [{ type: "agent", key: "turn:r1:1", reasoning: "thinking" }],
  });
  patch(11, [
    { op: "append", path: "/run/partial", value: "answer" },
    { op: "append", path: "/chat/turn:r1:1/text", value: "answer" },
  ], 10);
  assert.equal(store.sessions.main.run.partial, "answer");
  assert.equal(store.sessions.main.chat[0].text, "answer");
  assert.equal(store.sessions.main.cursor.offset, 11);
});

test("projection replacement cannot merge stale budget/tool fields", () => {
  snapshot({ tools: [{ name: "removed", marginal_tokens: 145 }], budget: { tool_marginal_tokens: { removed: 145 } } });
  patch(11, [
    { op: "replace", path: "/tools", value: [{ name: "read_file", marginal_tokens: 20 }] },
    { op: "replace", path: "/budget", value: { categories: { tools: 100 }, tool_marginal_tokens: { read_file: 20 } } },
  ], 10);
  assert.deepEqual(store.sessions.main.tools.map((tool) => tool.name), ["read_file"]);
  assert.equal(store.sessions.main.budget.tool_marginal_tokens.removed, undefined);
});

test("mutable messages and immutable History records arrive as separate projected fields", () => {
  snapshot();
  const original = { type: "message.appended", data: { message: { id: "result-1", content: "complete tool result" } } };
  patch(11, [
    { op: "replace", path: "/messages", value: [{ id: "result-1", content: "[elided: complete tool result]", elided: true }] },
    { op: "replace", path: "/timeline", value: [original] },
  ], 10);
  assert.equal(store.sessions.main.messages[0].content, "[elided: complete tool result]");
  assert.equal(store.sessions.main.timeline[0].data.message.content, "complete tool result");
});

test("turn-two parallel approval projects independently of sibling tool rows", () => {
	const chat = [
		{ type: "user", key: "message:first", text: "first turn" },
		{ type: "user", key: "message:second", text: "second turn" },
		{ type: "tool", key: "tool:list", text: "running" },
		{ type: "tool", key: "tool:read", text: "running" },
	];
	snapshot({ chat });
	const pending = { type: "notice", key: "event:1993", event: { type: "approval.required", data: { call_id: "shell-2", name: "shell.operator_command" } } };
	patch(11, [
		{ op: "replace", path: "/pending_approval", value: pending },
		{ op: "append", path: "/chat", value: pending },
	], 10);
	assert.equal(store.sessions.main.pending_approval.event.data.call_id, "shell-2");
	assert.equal(store.sessions.main.chat.length, 5);
	assert.equal(store.sessions.main.chat[2].key, "tool:list");
});

test("parallel stop updates preserve existing Chat rows", () => {
	const user = { type: "user", key: "message:user", text: "keep this visible" };
	snapshot({ chat: [user, { type: "tool", key: "tool:shell", text: "running" }, { type: "tool", key: "tool:read", text: "running" }] });
	patch(11, [
		{ op: "upsert", path: "/chat/tool:shell", value: { type: "tool", key: "tool:shell", text: "canceled" } },
		{ op: "upsert", path: "/chat/tool:read", value: { type: "tool", key: "tool:read", text: "canceled" } },
	], 10);
	assert.strictEqual(store.sessions.main.chat[0], user);
	assert.deepEqual(store.sessions.main.chat.map((entry) => entry.text), ["keep this visible", "canceled", "canceled"]);
});

test("queued count reconstructs from projected state", () => {
	snapshot({ queued_messages: 0 });
	patch(11, [{ op: "replace", path: "/queued_messages", value: 2 }], 10);
	assert.equal(store.sessions.main.queued_messages, 2);
});

test("projection-neutral tape events advance the cursor without rerendering views", () => {
  snapshot();
  let notifications = 0;
  const unsubscribe = subscribe((_state, event) => { if (event.type !== "init") notifications++; });
  patch(11, [], 10);
  assert.equal(store.sessions.main.cursor.offset, 11);
  assert.equal(notifications, 0);
  unsubscribe();
});

test("one agent and chat selection object persists across page loads", () => {
  snapshot();
  setSelection("agent_b", "main");
  assert.deepEqual(store.selection, { agent_id: "agent_b", session_id: "main" });
  assert.equal(stored.get("agentb.selection"), JSON.stringify(store.selection));
  reduce({ type: "snapshot", data: { sessions: store.sessions, replay: false, servers: [], config: {} } });
  assert.deepEqual(store.selection, { agent_id: "agent_b", session_id: "main" });
});

test("session-scoped UI error evidence never notifies rendering subscribers", () => {
  snapshot();
  let notifications = 0;
  const unsubscribe = subscribe((_state, event) => { if (event.type !== "init") notifications++; });
  reduce({ type: "error", data: { where: "ui", message: "render failed" } });
  assert.equal(store.error.message, "render failed");
  assert.equal(notifications, 0);
  reduce({ type: "error", data: { where: "server", message: "request failed" } });
  assert.equal(notifications, 1);
  unsubscribe();
});

test("successful probe projects a discovered context size into the live profile", () => {
  const profile = { id: "new", reasoning: { valid_efforts: [] }, context: { n_ctx: 0 }, capabilities: {} };
  reduce({ type: "snapshot", data: { sessions: {}, servers: [profile], config: { servers: [structuredClone(profile)] } } });
  reduce({ type: "server.probed", data: { server_id: "new", capabilities: { n_ctx: 32768, valid_efforts: ["low"] } } });
  assert.equal(store.servers[0].context.n_ctx, 32768);
  assert.equal(store.config.servers[0].context.n_ctx, 32768);
  assert.deepEqual(store.servers[0].reasoning.valid_efforts, ["low"]);
});

test("pending agent server changes are projected and cleared by terminal events", () => {
  snapshot();
  const change = { agent_id: "coder", from: "old", to: "new", requested_at: "now" };
  reduce({ type: "agent.server_change", data: { status: "pending", change } });
  assert.deepEqual(store.agent_server_changes.coder, change);
  reduce({ type: "agent.server_change", data: { status: "cancelled", agent_id: "coder" } });
  assert.equal(store.agent_server_changes.coder, undefined);
});
