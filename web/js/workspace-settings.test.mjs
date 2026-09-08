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
