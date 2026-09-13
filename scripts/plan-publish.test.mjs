import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import process from "node:process";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { preparePublication, publishPublication, workerStopped } from "./plan-publish.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const linter = path.join(here, "plan-lint.mjs");
const publisher = path.join(here, "plan-publish.mjs");
const linterHash = crypto.createHash("sha256").update(fs.readFileSync(linter)).digest("hex");
const roots = [];
process.on("exit", () => {
  for (const root of roots) if (path.basename(root).startsWith("agentb-plan-publish-")) fs.rmSync(root, { recursive: true, force: true });
});

function item(id, { state = "live", authorization = "operator", acceptance = "Fixture behavior is verified." } = {}) {
  return [
    `state: ${state}`,
    "milestone: 0.2",
    "kind: feature",
    "surfaces: plan, tests",
    `authorization: ${authorization}`,
    "evidence: Operator-authorized publication fixture.",
    ...(acceptance === null ? [] : [`acceptance: ${acceptance}`]),
    ...(state === "shipped" ? ["shipped: v0.1.0 abcdef0"] : []),
    "",
    `# ${id} — fixture item`,
    "",
    "Fixture body.",
    "",
    "## Unresolved",
    "",
    "(none)",
    "",
  ].join("\n");
}

function plan(order, inFlight = "TEST/W0 completed 12:00") {
  return [
    "# Plan fixture", "", "## Current work order — TEST", "", "Order ID: `TEST`", "", order, "",
    "## In flight", "", inFlight, "", "## Index", "", "placeholder", "",
  ].join("\n");
}

function makeRoot() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "agentb-plan-publish-root-"));
  roots.push(root);
  fs.mkdirSync(path.join(root, "plan", "items"), { recursive: true });
  fs.mkdirSync(path.join(root, "plan", "archive"), { recursive: true });
  fs.writeFileSync(path.join(root, "plan", "_reference.md"), "# References\n");
  fs.writeFileSync(path.join(root, "plan", "_history.md"), "# History\n");
  fs.writeFileSync(path.join(root, "plan", "items", "2a.md"), item("2a"));
  fs.writeFileSync(path.join(root, "PLAN.md"), plan("- W1 **2a existing work.**"));
  const prepared = spawnSync(process.execPath, [linter, "--root", root, "--write-index", "--structural"], { encoding: "utf8" });
  assert.equal(prepared.status, 0, prepared.stdout + prepared.stderr);
  return root;
}

function makeCandidate(root, order, entries = []) {
  const candidate = fs.mkdtempSync(path.join(os.tmpdir(), "agentb-plan-publish-candidate-"));
  roots.push(candidate);
  fs.writeFileSync(path.join(candidate, "PLAN.md"), plan(order));
  for (const { where = "items", id, text } of entries) {
    fs.mkdirSync(path.join(candidate, "plan", where), { recursive: true });
    fs.writeFileSync(path.join(candidate, "plan", where, `${id}.md`), text);
  }
  return candidate;
}

function activeBytes(root) {
  const files = [path.join(root, "PLAN.md"), ...["items", "archive"].flatMap((label) => fs.readdirSync(path.join(root, "plan", label)).map((name) => path.join(root, "plan", label, name)))];
  return Object.fromEntries(files.sort().map((full) => [path.relative(root, full), fs.readFileSync(full, "utf8")]));
}

function expectPrepareFailure(root, candidate, pattern) {
  const before = activeBytes(root);
  assert.throws(() => preparePublication({ root, candidate }), pattern);
  assert.deepEqual(activeBytes(root), before, "failed preparation must leave active state byte-unchanged");
}

{
  const root = makeRoot();
  const candidate = makeCandidate(root, "- W1 **2a existing work.**\n- W2 **2b added work.**", [{ id: "2b", text: item("2b") }]);
  const prepared = preparePublication({ root, candidate });
  assert.match(prepared.manifest.proposal_id, /^sha256:[0-9a-f]{64}$/);
  assert.match(fs.readFileSync(path.join(candidate, "PLAN.md"), "utf8"), /\[2b\]\(plan\/items\/2b\.md\)/, "preparation must generate the candidate index");
  const published = publishPublication({ root, candidate });
  assert.deepEqual(published.files.sort(), ["PLAN.md", "plan/items/2b.md"]);
  assert.equal(fs.readFileSync(path.join(root, "PLAN.md"), "utf8"), fs.readFileSync(path.join(candidate, "PLAN.md"), "utf8"), "published PLAN must be the exact validated bytes");
  assert.equal(fs.readFileSync(path.join(root, "plan", "items", "2b.md"), "utf8"), fs.readFileSync(path.join(candidate, "plan", "items", "2b.md"), "utf8"), "published item must be the exact validated bytes");
  const ordinary = spawnSync(process.execPath, [linter, "--root", root], { encoding: "utf8" });
  assert.equal(ordinary.status, 0, ordinary.stdout + ordinary.stderr);
}

{
  const root = makeRoot();
  expectPrepareFailure(root, makeCandidate(root, "- W1 **2b invalid authorization.**", [{ id: "2b", text: item("2b", { authorization: "planner" }) }]), /invalid authorization/);
}

{
  const root = makeRoot();
  expectPrepareFailure(root, makeCandidate(root, "- W1 **2b missing acceptance.**", [{ id: "2b", text: item("2b", { acceptance: null }) }]), /missing required metadata acceptance/);
}

{
  const root = makeRoot();
  const candidate = makeCandidate(root, "- W1 **2b duplicate order body.**", [{ id: "2b", text: item("2b") }]);
  fs.appendFileSync(path.join(candidate, "PLAN.md"), "\n## Current work order — duplicate\n\nOrder ID: `OTHER`\n");
  expectPrepareFailure(root, candidate, /found 2 Current work order bodies/);
}

{
  const root = makeRoot();
  const candidate = makeCandidate(root, "- W1 **2a existing work.**");
  preparePublication({ root, candidate });
  fs.writeFileSync(path.join(root, "PLAN.md"), fs.readFileSync(path.join(root, "PLAN.md"), "utf8").replace("TEST/W0 completed 12:00", "TEST/W1 started 12:01"));
  const before = activeBytes(root);
  assert.throws(() => publishPublication({ root, candidate }), /worker is active in W1/);
  assert.deepEqual(activeBytes(root), before, "running-worker refusal must leave active state byte-unchanged");
}

{
  const root = makeRoot();
  const candidate = makeCandidate(root, "- W1 **2a existing work.**");
  preparePublication({ root, candidate });
  fs.appendFileSync(path.join(root, "PLAN.md"), "\n");
  const before = activeBytes(root);
  assert.throws(() => publishPublication({ root, candidate }), /active plan state changed after preparation/);
  assert.deepEqual(activeBytes(root), before, "changed-base refusal must leave active state byte-unchanged");
}

{
  const root = makeRoot();
  fs.writeFileSync(path.join(root, "plan", "archive", "2z.md"), item("2z", { state: "shipped" }));
  const candidate = makeCandidate(root, "- W1 **2z stale archived work.**");
  expectPrepareFailure(root, candidate, /RECONCILE: archived shipped item 2z is missing completed implementation marker\(s\): W1/);
}

assert.equal(workerStopped(plan("- W1 **2a work.**", "TEST/W1 started 12:00")).stopped, false);
assert.equal(workerStopped(plan("- W1 **2a work.**", "TEST/W1 started 12:00\nTEST/W1 completed 12:01")).stopped, true);
assert.equal(crypto.createHash("sha256").update(fs.readFileSync(linter)).digest("hex"), linterHash, "plan-lint.mjs must remain byte-unchanged");
assert.equal(fs.existsSync(publisher), true);

process.stdout.write("plan publication fixtures passed\n");
