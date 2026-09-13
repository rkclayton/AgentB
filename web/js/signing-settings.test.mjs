import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const source = (await Promise.all([
  "settings.js", "settings-connections.js", "settings-general.js", "settings-context.js", "settings-run.js",
  "settings-delivery.js", "settings-about.js", "settings-workspace.js", "settings-security.js",
].map((name) => readFile(new URL(`./${name}`, import.meta.url), "utf8")))).join("\n");

test("Security signing onboarding is gated and standard users see Verify only", () => {
  assert.match(source, /Create certificate/);
  assert.match(source, /Import certificate/);
  assert.match(source, /Sign application/);
  assert.match(source, /Verify signatures/);
  assert.match(source, /signingStatus\.can_manage/);
  assert.match(source, /Standard users can verify signatures but cannot create/);
  assert.match(source, /stable publisher identity and trusted local chain/);
});
