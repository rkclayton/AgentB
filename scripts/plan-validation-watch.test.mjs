import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import process from "node:process";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { loadPublishedProposal } from "./plan-lint.mjs";
import {
  processRequestFile,
  readValidationResult,
  readWatcherStatus,
  resultPathFor,
  startValidationWatcher,
} from "./plan-validation-watch.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const linter = path.join(here, "plan-lint.mjs");
const roots = [];
process.on("exit", () => {
  for (const root of roots) {
    const relative = path.relative(os.tmpdir(), root);
    if (!relative.startsWith("..") && path.basename(root).startsWith("agentb-plan-watch-")) fs.rmSync(root, { recursive: true, force: true });
  }
});

function item(id, body = "Fixture body.") {
  return [
    "state: live",
    "milestone: 0.2",
    "kind: feature",
    "surfaces: plan, tests",
    "evidence: Operator-authorized fixture scope.",
    "acceptance: Fixture behavior is verified.",
    "",
    `# ${id} — fixture item`,
    "",
    body,
    "",
    "## Unresolved",
    "",
    "(none)",
    "",
  ].join("\n");
}

function fixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "agentb-plan-watch-"));
  roots.push(root);
  fs.mkdirSync(path.join(root, "plan", "items"), { recursive: true });
  fs.mkdirSync(path.join(root, "plan", "archive"), { recursive: true });
  fs.writeFileSync(path.join(root, "plan", "items", "2a.md"), item("2a"));
  fs.writeFileSync(path.join(root, "PLAN.md"), [
    "# Plan fixture",
    "",
    "## Current work order — TEST",
    "",
    "**Revision: r1.**",
    "",
    "Order ID: `TEST`",
    "",
    "- W1 **2a executable work.**",
    "",
    "## In flight",
    "",
    "- TEST/W0 completed 12:00",
    "",
    "## Index",
    "",
    "placeholder",
    "",
  ].join("\n"));
  const prepared = spawnSync(process.execPath, [linter, "--root", root, "--write-index", "--structural"], { encoding: "utf8" });
  assert.equal(prepared.status, 0, prepared.stdout + prepared.stderr);
  return root;
}

function proposalRequest(root, { newItem = false, invalid = false, commandText = "" } = {}) {
  const published = loadPublishedProposal(root);
  const contents = published.itemContents.map((entry) => ({ ...entry }));
  let orderBody = ["**Revision: r1.**", "", "Order ID: `TEST`", "", "- W1 **2a executable work.**"].join("\n");
  if (newItem) {
    contents.push({ relative: "plan/items/2b.md", text: item("2b", commandText || "New proposed work.") });
    orderBody = ["**Revision: r1.**", "", "Order ID: `TEST`", "", "- W1 **2b executable work.**"].join("\n");
  }
  if (invalid) {
    const target = contents.find((entry) => entry.relative.endsWith("2a.md"));
    target.text = target.text.replace("kind: feature", "kind: impossible");
  }
  return {
    version: 1,
    operation: "validateProposal",
    proposal: { planText: published.planText, orderBody, itemContents: contents },
  };
}

function baseProposalRequest({ invalid = false, commandText = "" } = {}) {
  const text = item("2b", commandText || "New proposed work.");
  return {
    version: 1,
    operation: "validateProposal",
    proposal: {
      base: "published",
      orderBody: ["**Revision: r1.**", "", "Order ID: `TEST`", "", "- W1 **2b executable work.**"].join("\n"),
      itemContents: { "plan/items/2b.md": invalid ? text.replace("## Unresolved\n\n(none)", "## Unresolved\n\nUnclassified question.") : text },
    },
  };
}

function publishBaseRequest(root, request) {
  const planPath = path.join(root, "PLAN.md");
  const planText = fs.readFileSync(planPath, "utf8");
  const current = planText.match(/^## Current work order[^\n]*\n[\s\S]*?(?=^## (?:Next work order|In flight|Index)|(?![\s\S]))/m);
  assert.ok(current, "fixture must have a Current work order");
  const heading = current[0].match(/^## Current work order[^\n]*/)[0];
  fs.writeFileSync(planPath, `${planText.slice(0, current.index)}${heading}\n\n${request.proposal.orderBody}\n\n${planText.slice(current.index + current[0].length)}`);
  for (const [relative, text] of Object.entries(request.proposal.itemContents)) {
    const target = path.join(root, ...relative.split("/"));
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.writeFileSync(target, text);
  }
  const prepared = spawnSync(process.execPath, [linter, "--root", root, "--write-index", "--structural"], { encoding: "utf8" });
  assert.equal(prepared.status, 0, prepared.stdout + prepared.stderr);
}

function cliErrors(run) {
  return run.stderr.split(/\r?\n/).filter((line) => line.startsWith("ERROR ")).map((line) => line.slice(6));
}

async function waitForResult(requestPath, timeoutMs = 2000, publishedRoot = undefined) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const state = readValidationResult(requestPath, publishedRoot ? { publishedRoot } : undefined);
    if (state.state !== "absent" && state.state !== "stale") return state;
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  throw new Error(`timed out waiting for ${resultPathFor(requestPath)}`);
}

const root = fixture();
const drop = path.join(root, "plan", "validation");
const originalPlan = fs.readFileSync(path.join(root, "PLAN.md"), "utf8");
const originalItem = fs.readFileSync(path.join(root, "plan", "items", "2a.md"), "utf8");
assert.equal(readWatcherStatus(drop).state, "absent");
const watcher = startValidationWatcher({ dropDirectory: drop, publishedRoot: root });
assert.equal(readWatcherStatus(drop).state, "ready");

// A proposal that adds an item validates against its generated in-memory index.
const validPath = path.join(drop, "valid.request.json");
const validText = JSON.stringify(proposalRequest(root, { newItem: true }), null, 2);
fs.writeFileSync(validPath, validText);
const valid = await waitForResult(validPath);
assert.equal(valid.state, "pass", JSON.stringify(valid.result?.validation?.errors));
assert.match(valid.result.request.sha256, /^sha256:[0-9a-f]{64}$/);
assert.equal(valid.result.request.text, validText, "result must echo the exact checked UTF-8 input");
assert.doesNotMatch(valid.result.validation.errors.join("\n"), /index: stale or malformed/);
assert.equal(fs.readFileSync(path.join(root, "PLAN.md"), "utf8"), originalPlan, "in-memory index regeneration must not publish PLAN.md");
assert.equal(fs.existsSync(path.join(root, "plan", "items", "2b.md")), false, "proposal item must not be published");

// Base mode carries one changed item and an order body, then inherits all
// other validator inputs from the published tree without writing through.
const basePath = path.join(drop, "base.request.json");
const baseText = JSON.stringify(baseProposalRequest(), null, 2);
fs.writeFileSync(basePath, baseText);
const base = await waitForResult(basePath, 2000, root);
assert.equal(base.state, "pass", JSON.stringify(base.result?.validation?.errors));
assert.ok(Buffer.byteLength(baseText) < Buffer.byteLength(validText), "published base must make the request smaller than an inline proposal");
assert.deepEqual(base.result.published.inputs.map(({ relative }) => relative), ["PLAN.md", "plan/items/2a.md"]);
assert.equal(fs.readFileSync(path.join(root, "PLAN.md"), "utf8"), originalPlan, "base merge must not publish its order body");
assert.equal(fs.readFileSync(path.join(root, "plan", "items", "2a.md"), "utf8"), originalItem, "base merge must not modify published items");
assert.equal(fs.existsSync(path.join(root, "plan", "items", "2b.md")), false, "base merge must not publish an added item");

// Invalid proposals retain actionable field-level diagnostics.
const invalidPath = path.join(drop, "invalid.request.json");
fs.writeFileSync(invalidPath, JSON.stringify(proposalRequest(root, { invalid: true }), null, 2));
const invalid = await waitForResult(invalidPath);
assert.equal(invalid.state, "fail");
assert.ok(invalid.result.validation.errorDetails.some(({ field, expected }) => field === "kind" && expected.includes("defect")));

// The second existing validator is reachable through the same file-only path.
const published = loadPublishedProposal(root);
const resumePath = path.join(drop, "resume.request.json");
fs.writeFileSync(resumePath, JSON.stringify({
  version: 1,
  operation: "validateResume",
  resume: {
    acceptedParts: [{ revision: "r1", takenAt: "2026-09-10T00:00:00Z", planText: published.planText, itemContents: published.itemContents }],
    published,
  },
}, null, 2));
const resume = await waitForResult(resumePath);
assert.equal(resume.state, "pass", JSON.stringify(resume.result?.validation?.errors));

watcher.close();
assert.equal(readWatcherStatus(drop).state, "stopped");

// A changed published input invalidates a base-mode result even when the
// exact request bytes have not changed.
fs.writeFileSync(path.join(root, "plan", "items", "2a.md"), `${originalItem}\n`);
assert.equal(readValidationResult(basePath, { publishedRoot: root }).state, "stale");
fs.writeFileSync(path.join(root, "plan", "items", "2a.md"), originalItem);
assert.equal(readValidationResult(basePath, { publishedRoot: root }).state, "pass");

// A pre-base-mode result has no published-input manifest and cannot be read
// as covering a request that names the published tree.
const currentBaseResult = JSON.parse(fs.readFileSync(resultPathFor(basePath), "utf8"));
delete currentBaseResult.published;
fs.writeFileSync(resultPathFor(basePath), `${JSON.stringify(currentBaseResult, null, 2)}\n`);
assert.equal(readValidationResult(basePath, { publishedRoot: root }).state, "stale");
processRequestFile(basePath, { publishedRoot: root });
assert.equal(readValidationResult(basePath, { publishedRoot: root }).state, "pass");

// A changed request cannot inherit a result produced for earlier bytes.
fs.writeFileSync(validPath, `${validText}\n`);
assert.equal(readValidationResult(validPath).state, "stale");

// With no resident watcher, a new request has no result and cannot be mistaken for a pass.
const absentPath = path.join(drop, "absent.request.json");
fs.writeFileSync(absentPath, JSON.stringify(proposalRequest(root)));
assert.equal(readValidationResult(absentPath).state, "absent");

// Each explicit attempt to cross the validation-only boundary is refused.
for (const [field, value] of [
  ["publish", { path: "PLAN.md" }],
  ["modifyItem", { path: "plan/items/2a.md" }],
  ["startOrder", "TEST"],
  ["execute", "touch boundary-executed"],
]) {
  const requestPath = path.join(drop, `${field}.request.json`);
  fs.writeFileSync(requestPath, JSON.stringify({ ...proposalRequest(root), [field]: value }, null, 2));
  const { result } = processRequestFile(requestPath);
  assert.equal(result.status, "refused", `${field} must be refused`);
  assert.match(result.errors[0].message, new RegExp(field));
}

// Replacing a result path unlinks it first, so even a hard link cannot turn a
// verdict write into a PLAN.md write.
const linkedPath = path.join(drop, "linked-result.request.json");
fs.writeFileSync(linkedPath, JSON.stringify(proposalRequest(root), null, 2));
fs.linkSync(path.join(root, "PLAN.md"), resultPathFor(linkedPath));
processRequestFile(linkedPath);
assert.equal(fs.readFileSync(path.join(root, "PLAN.md"), "utf8"), originalPlan, "an existing result link must not redirect a write into PLAN.md");
assert.equal(readValidationResult(linkedPath).state, "pass");

// Command-like proposal contents remain inert data while validation runs.
const sentinel = path.join(root, "boundary-executed");
const inertPath = path.join(drop, "inert-command.request.json");
const command = `node -e \"require('node:fs').writeFileSync(${JSON.stringify(sentinel)}, 'ran')\"`;
fs.writeFileSync(inertPath, JSON.stringify(proposalRequest(root, { newItem: true, commandText: command }), null, 2));
const inert = processRequestFile(inertPath).result;
assert.equal(inert.status, "pass", JSON.stringify(inert.validation?.errors));
assert.equal(fs.existsSync(sentinel), false, "proposal contents must never execute");

assert.equal(fs.readFileSync(path.join(root, "PLAN.md"), "utf8"), originalPlan, "watcher must never publish or start an order");
assert.equal(fs.readFileSync(path.join(root, "plan", "items", "2a.md"), "utf8"), originalItem, "watcher must never modify an item");

// Base mode and CLI admission call the same validator over the same effective
// plan on both sides of the admission boundary.
for (const invalid of [false, true]) {
  const baseRoot = fixture();
  const request = baseProposalRequest({ invalid });
  const requestPath = path.join(baseRoot, "plan", "validation", `parity-${invalid ? "fail" : "pass"}.request.json`);
  fs.mkdirSync(path.dirname(requestPath), { recursive: true });
  fs.writeFileSync(requestPath, JSON.stringify(request, null, 2));
  const baseResult = processRequestFile(requestPath, { publishedRoot: baseRoot }).result;

  const cliRoot = fixture();
  publishBaseRequest(cliRoot, request);
  const cli = spawnSync(process.execPath, [linter, "--root", cliRoot], { encoding: "utf8" });
  assert.equal(baseResult.status === "pass", cli.status === 0, `base and CLI status must agree for invalid=${invalid}`);
  assert.deepEqual(baseResult.validation.errors, cliErrors(cli), `base and CLI diagnostics must agree for invalid=${invalid}`);
}

process.stdout.write("plan validation watcher fixtures passed\n");
