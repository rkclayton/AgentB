import assert from "node:assert/strict";
import test from "node:test";
import { approvalChoices, approvalText, createApprovalCard, shellGrantApproval } from "./approval.js";

test("both approval kinds expose exactly chat, once, and no in order", () => {
  for (const data of [
    { name: "shell", boundary_escape: false },
    { name: "shell.operator_override", boundary_escape: true },
    { name: "shell.operator_command", boundary_escape: true },
  ]) {
    assert.equal(shellGrantApproval(data), true);
		assert.deepEqual(approvalChoices(data), [["session", "Yes, for this chat"], ["once", "Just once"], ["deny", "No"]]);
	}
});

test("file escape and policy cards stay within the same three-button vocabulary", () => {
	assert.deepEqual(approvalChoices({ name: "write_file.operator_override", boundary_escape: true }), [["session", "Yes, for this chat"], ["once", "Just once"], ["deny", "No"]]);
	assert.deepEqual(approvalChoices({ name: "write_file", boundary_escape: false }), [["session", "Yes, for this chat"], ["once", "Just once"], ["deny", "No"]]);
});

test("approval wording stays direct and identifies the operation", () => {
	const identity = approvalText({ name: "shell.operator_command", boundary_escape: true, args: { reason: "git runs as you", command: "git diff" } });
	assert.equal(identity.title, "Run as you");
	assert.match(identity.reason, /git runs as you/);
	assert.equal(identity.detail, "git diff");
	const policy = approvalText({ name: "write_file", boundary_escape: false, args: { path: "note.txt" } });
	assert.equal(policy.title, "Allow this");
	assert.equal(policy.detail, "note.txt");
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
		assert.equal(card.children.some((child) => child.tag === "div"), false);
		assert.match(card.children.map((child) => child.text || child.textContent || "").join(""), new RegExp(decision));
	}
});
