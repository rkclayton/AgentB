import assert from "node:assert/strict";
import test from "node:test";

globalThis.document = { hidden: false, getElementById: () => null, addEventListener: () => {} };
globalThis.window = { addEventListener: () => {} };
globalThis.EventSource = class { addEventListener() {} };
const { reduce, store } = await import("./bus.js");

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
