import assert from "node:assert/strict";
import test from "node:test";
import { approvalChoices, approvalText, createApprovalCard, shellGrantApproval } from "./approval.js";

test("shell approval cards expose once, run, chat, and explicit operator mode in order", () => {
  for (const data of [
    { name: "shell", boundary_escape: false },
    { name: "shell.operator_override", boundary_escape: true },
    { name: "shell.operator_command", boundary_escape: true },
  ]) {
    assert.equal(shellGrantApproval(data), true);
		assert.deepEqual(approvalChoices(data).map(([value]) => value), ["once", "run", "session", "operator_mode", "deny"]);
		assert.deepEqual(approvalChoices(data).map(([, label]) => label), ["Once", "For this run", "For this chat", "Operator mode", "Keep denied"]);
	}
});

test("file escape cards expose once, run, and chat but never shell operator mode", () => {
	assert.deepEqual(approvalChoices({ name: "write_file.operator_override", boundary_escape: true }).map(([value]) => value), ["once", "run", "session", "deny"]);
	assert.deepEqual(approvalChoices({ name: "write_file", boundary_escape: false }).map(([value]) => value), ["approve", "deny"]);
});

test("approval wording stays direct and identifies the operation", () => {
	assert.match(approvalText({ name: "shell.operator_command", boundary_escape: true, args: { reason: "git runs as you", command: "git diff" } }), /git runs as you.*git diff/);
	assert.equal(approvalText({ name: "write_file", boundary_escape: false, args: { path: "note.txt" } }), "Policy confirmation: write_file note.txt");
});

test("replayed superseded and dismissed cards are recorded but never actionable", () => {
	const document = {
		createTextNode: (text) => ({ text }),
		createElement: (tag) => ({ tag, children: [], className: "", classList: { add() {} }, append(...children) { this.children.push(...children); } }),
	};
	for (const decision of ["superseded", "dismissed"]) {
		const card = createApprovalCard(document, {
			decision,
			event: { data: { call_id: "call", name: "read_file.operator_override", boundary_escape: true, args: { path: "outside.txt" } } },
		}, { replay: true, decide: () => assert.fail("replay decision invoked") });
		assert.equal(card.children.some((child) => child.tag === "button"), false);
		assert.match(card.children.map((child) => child.text || "").join(""), new RegExp(decision));
	}
});
