import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import process from "node:process";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { loadPublishedProposal, validateProposal } from "./plan-lint.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const linter = path.join(here, "plan-lint.mjs");
const fixtureRoots = [];
process.on("exit", () => {
  for (const root of fixtureRoots) {
    const relative = path.relative(os.tmpdir(), root);
    if (!relative.startsWith("..") && path.basename(root).startsWith("agentb-plan-lint-")) fs.rmSync(root, { recursive: true, force: true });
  }
});

function item(id, { state = "live", unknown = false, unresolved = "(none)" } = {}) {
  const resolved = unknown ? "unknown" : null;
  return [
    `state: ${state}`,
    `milestone: ${resolved ?? "0.2"}`,
    `kind: ${resolved ?? "feature"}`,
    `surfaces: ${resolved ?? "chat"}`,
    `evidence: ${resolved ?? "Operator-authorized fixture scope."}`,
    `acceptance: ${resolved ?? "Fixture behavior is verified."}`,
    "",
    `# ${id} — fixture item`,
    "",
    "Fixture body.",
    "",
    "## Unresolved",
    "",
    unresolved,
    "",
  ].join("\n");
}

function makeFixture(current, entries, { next = true, inFlight = "TEST/W0 started" } = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "agentb-plan-lint-"));
  fixtureRoots.push(root);
  fs.mkdirSync(path.join(root, "plan", "items"), { recursive: true });
  fs.mkdirSync(path.join(root, "plan", "archive"), { recursive: true });
  fs.writeFileSync(path.join(root, "plan", "_reference.md"), "# References\n");
  fs.writeFileSync(path.join(root, "plan", "_history.md"), "# History\n");
  for (const entry of entries) fs.writeFileSync(path.join(root, "plan", entry.where, `${entry.id}.md`), entry.text);
  fs.writeFileSync(path.join(root, "PLAN.md"), [
    "# Plan fixture", "", "## Current work order — TEST", "", "Order ID: `TEST`", current, "",
    ...(next ? ["## Next work order — later", "", "Nothing queued.", ""] : []),
    "## In flight", "", `- ${inFlight}`, "", "## Index", "", "placeholder", "",
  ].join("\n"));
  return root;
}

function run(root, ...args) {
  return spawnSync(process.execPath, [linter, "--root", root, ...args], { encoding: "utf8" });
}

function prepare(root) {
  const result = run(root, "--write-index", "--structural");
  assert.equal(result.status, 0, result.stdout + result.stderr);
}

{
  const root = makeFixture("\n- W1 **2a closure check.**", [{ id: "2a", where: "archive", text: item("2a", { state: "shipped" }).replace("evidence: Operator-authorized fixture scope.", "shipped: v0.1.0 abcdef0\nevidence: Recorded release evidence.") }]);
  prepare(root);
  const result = run(root, "--structural");
  assert.equal(result.status, 0, result.stdout + result.stderr);
}

{
  const root = makeFixture("\n- W1 **2a proposed work.**", [{ id: "2a", where: "items", text: item("2a", { state: "proposed", unknown: true }) }]);
  prepare(root);
  const result = run(root);
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /ORDER GATE: executable item 2a is proposed/);
  assert.match(result.stderr, /unresolved milestone/);
}

{
  const root = makeFixture("\n- W1 **2a observed but unapproved work.**", [{ id: "2a", where: "items", text: item("2a").replace("Operator-authorized fixture scope.", "Observed fixture behavior; approval status is ambiguous.") }]);
  prepare(root);
  const result = run(root);
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /no recorded authorization/);
}

{
  const root = makeFixture("\nRequired reading: archived [[2b]] for context only.\n\n- W1 **2a executable work.**", [
    { id: "2a", where: "items", text: item("2a") },
    { id: "2b", where: "archive", text: item("2b", { state: "shipped" }).replace("evidence: Operator-authorized fixture scope.", "shipped: v0.1.0 abcdef0\nevidence: Recorded release evidence.") },
  ]);
  prepare(root);
  const result = run(root);
  assert.equal(result.status, 0, result.stdout + result.stderr);
}

{
  const root = makeFixture("\n- W1 **2a executable work.**", [{ id: "2a", where: "items", text: item("2a") }], { next: false });
  prepare(root);
  const result = run(root);
  assert.equal(result.status, 0, result.stdout + result.stderr);
}

{
  const root = makeFixture("\n- W1 **2ah root cause.**\n- W2 **2ai prose.**\n- W3 **2aj grant.**\n- W4 **2ak tab.**\n- W5 **2am lamp.**", [
    { id: "2ah", where: "items", text: item("2ah", { unresolved: "[discovery] Establish the render cause." }) },
    { id: "2ai", where: "items", text: item("2ai") },
    { id: "2aj", where: "items", text: item("2aj", { unresolved: "[discovery] Establish the grant key." }) },
    { id: "2ak", where: "items", text: item("2ak", { unresolved: "[discovery] Establish current click behavior." }) },
    { id: "2am", where: "items", text: item("2am", { unresolved: "[discovery] Establish the negative reachability signal." }) },
  ]);
  prepare(root);
  const result = run(root);
  assert.equal(result.status, 0, result.stdout + result.stderr);
  assert.match(result.stderr, /explicitly covered by discovery-first work/);
}

{
  const root = makeFixture("\n- W1 **2a executable work.**", [{ id: "2a", where: "items", text: item("2a", { unresolved: "[blocker] Required fixture service is unavailable." }) }]);
  prepare(root);
  const validation = validateProposal(loadPublishedProposal(root));
  assert.equal(validation.admission.errors.length, 0, "a runtime blocker must not be mislabeled as an admission failure");
  assert.equal(validation.blockers.length, 1);
  assert.match(validation.blockers[0].message, /ORDER BLOCKER: item 2a/);
  const result = run(root);
  assert.notEqual(result.status, 0, "a genuine blocker must pause execution");
}

{
  const root = makeFixture("\n- W1 **2a executable work.**", [{ id: "2a", where: "items", text: item("2a") }], { inFlight: "TEST/W1 completed 12:00" });
  prepare(root);
  const validation = validateProposal({ ...loadPublishedProposal(root), structuralOnly: true });
  assert.equal(validation.completion[0].status, "partial", "a completion marker alone must not close a live item");
}

{
  const shipped = item("2a", { state: "shipped" }).replace("evidence: Operator-authorized fixture scope.", "shipped: v0.1.0 abcdef0\nevidence: Recorded acceptance evidence.");
  const root = makeFixture("\n- W1 **2a executable work.**", [{ id: "2a", where: "archive", text: shipped }], { inFlight: "TEST/W1 completed 12:00" });
  prepare(root);
  const validation = validateProposal({ ...loadPublishedProposal(root), structuralOnly: true });
  assert.equal(validation.completion[0].status, "complete", "an archived shipped item with acceptance evidence should close");
}

{
  const root = makeFixture("\nNo product changes.\n\n- W1 Inspect.", []);
  prepare(root);
  fs.writeFileSync(path.join(root, "PLAN.md"), fs.readFileSync(path.join(root, "PLAN.md"), "utf8").replace("- TEST/W0 started", "- OTHER/W0 started"));
  const result = run(root, "--structural");
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /belongs to another order/);
}

{
  const root = makeFixture("\nNo product changes.\n\n- W1 Inspect.", []);
  prepare(root);
  fs.writeFileSync(path.join(root, "PLAN.md"), fs.readFileSync(path.join(root, "PLAN.md"), "utf8").replace("## Index", "## Completed work order — old\n\nClosed.\n\n## Index"));
  const result = run(root, "--structural");
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /completed-order heading is not allowed/);
}

{
  const root = makeFixture("\n- W1 **2a executable work.**", [{ id: "2a", where: "items", text: item("2a") }]);
  prepare(root);
  const published = loadPublishedProposal(root);
  const orderBody = published.planText.match(/^## Current work order[^\n]*\n([\s\S]*?)(?=^## (?:Next work order|In flight|Index))/m)[1].trim();
  const proposedItems = published.itemContents.map((entry) => entry.relative === "plan/items/2a.md"
    ? { ...entry, text: item("2a", { state: "proposed", unknown: true }) }
    : entry);
  const proposed = validateProposal({ ...published, orderBody, itemContents: proposedItems });
  assert.match(proposed.errors.join("\n"), /ORDER GATE: executable item 2a is proposed/);
  assert.match(proposed.errors.join("\n"), /unresolved milestone/);
  assert.ok(proposed.errorDetails.every(({ field, expected }) => field && expected), "proposal errors must carry an offending field and expected form");
  assert.match(proposed.proposalId, /^sha256:[0-9a-f]{64}$/);
  const changed = validateProposal({ ...published, orderBody: `${orderBody}\n\nChanged proposal.`, itemContents: proposedItems });
  assert.notEqual(changed.proposalId, proposed.proposalId, "a changed proposal must not reuse a stale validation identity");
  assert.equal(fs.readFileSync(path.join(root, "plan", "items", "2a.md"), "utf8"), item("2a"), "proposal validation must not publish item changes");
  assert.equal(fs.readFileSync(path.join(root, "PLAN.md"), "utf8").includes("Changed proposal."), false, "proposal validation must not publish the order body");

  fs.writeFileSync(path.join(root, "plan", "items", "2a.md"), proposedItems.find((entry) => entry.relative === "plan/items/2a.md").text);
  const publishedResult = validateProposal(loadPublishedProposal(root));
  assert.deepEqual(proposed.errors, publishedResult.errors, "proposed and published inputs must report the same validation errors");
}

process.stdout.write("plan-lint fixtures passed\n");
