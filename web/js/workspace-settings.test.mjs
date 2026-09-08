import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const settings = await readFile(new URL("./settings.js", import.meta.url), "utf8");

test("Settings Workspace is read-only for memory and retains policy controls", () => {
  assert.match(settings, /\["workspace", "Workspace"\]/);
  assert.match(settings, /memory_count/);
  assert.match(settings, /last_used/);
  assert.doesNotMatch(settings, /clear-workspace-memory/);
  assert.match(settings, /policy\.hash/);
  assert.match(settings, /policy\.approved_at/);
  assert.match(settings, /Confirm revoke/);
  assert.match(settings, /\/api\/workspaces\/policy-revoke/);
});

test("Settings Workspace owns operator attachments mailbox approvals retention and Adopt", () => {
	assert.match(settings, /row\("attachments"/);
	assert.match(settings, /data-action="empty-operator-attachments"/);
	assert.match(settings, /Confirm empty/);
	assert.match(settings, /operator_files\.allow_mailbox_approvals/);
	assert.match(settings, /whoever can write to your synced folder can then grant the agent your identity/);
	assert.match(settings, /operator_files\.log_retention_days/);
	assert.match(settings, /Adopt repository instructions/);
	assert.match(settings, /Also remove AGENTS\.md \/ CLAUDE\.md/);
	assert.match(settings, /Cleanup is destructive and is off by default/);
	assert.match(settings, /\/api\/operator-files/);
});
