import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { executeTool, materializeFixture, snapshotPaths } from "./run-trials.mjs";

const root = fs.mkdtempSync(path.join(os.tmpdir(), "agentb-stop-runner-test-"));
try {
  const fixture = { environment: { "nested/a.txt": "alpha\n", "b.txt": "beta\n" } };
  materializeFixture(fixture, root);
  const before = snapshotPaths(root, ["nested/a.txt", "b.txt"]);
  assert.equal(executeTool(root, "read_file", { path: "nested/a.txt" }).result, "alpha\n");
  assert.match(executeTool(root, "list_dir", {}).result, /nested\//);
  assert.equal(executeTool(root, "search_text", { pattern: "beta" }).result, "b.txt:1: beta");
  assert.equal(executeTool(root, "edit_file", { path: "nested/a.txt", old_string: "alpha", new_string: "gamma" }).ok, true);
  assert.equal(executeTool(root, "write_file", { path: "b.txt", content: "delta\n" }).ok, true);
  const after = snapshotPaths(root, ["nested/a.txt", "b.txt"]);
  assert.notEqual(before["nested/a.txt"], after["nested/a.txt"]);
  assert.notEqual(before["b.txt"], after["b.txt"]);
  assert.equal(executeTool(root, "read_file", { path: "../outside" }).ok, false);
  console.log("PASS stop-discipline runner materialization, tools, hashes, and path boundary");
} finally {
  fs.rmSync(root, { recursive: true, force: true });
}
