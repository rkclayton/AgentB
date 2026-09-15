import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const suiteRoot = path.dirname(fileURLToPath(import.meta.url));
const seedRoot = path.join(suiteRoot, "fixture", "seed");
const manifest = JSON.parse(fs.readFileSync(path.join(suiteRoot, "manifest.json"), "utf8"));
const go = process.env.AGENTB_GO || "C:\\projects\\AgentB\\.tools\\go\\bin\\go.exe";

function filesBelow(root) {
  const found = [];
  function walk(directory) {
    for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
      const absolute = path.join(directory, entry.name);
      if (entry.isDirectory()) walk(absolute);
      else found.push(path.relative(root, absolute).replaceAll("\\", "/"));
    }
  }
  walk(root);
  return found.sort();
}

function treeHash(root) {
  const hash = crypto.createHash("sha256");
  for (const relative of filesBelow(root)) {
    hash.update(relative);
    hash.update("\0");
    hash.update(fs.readFileSync(path.join(root, relative)));
    hash.update("\0");
  }
  return hash.digest("hex");
}

function runVerifier(task, cwd) {
  const executable = task.verifier.command === "go" ? go : task.verifier.command;
  return spawnSync(executable, task.verifier.args, { cwd, encoding: "utf8", windowsHide: true });
}

function normalize(value) {
  return value.toLowerCase().replaceAll("`", "").replace(/[^a-z0-9./^-]+/g, " ").trim();
}

function factTokens(value) {
  const stop = new Set(["a", "an", "and", "at", "do", "for", "in", "is", "not", "of", "or", "the", "to"]);
  return normalize(value).split(/\s+/).map((token) => token.replace(/[.,]+$/, "")).filter((token) => token && !stop.has(token)).map((token) => {
    if (token === "passes") return "pass";
    return token.length > 4 && token.endsWith("s") ? token.slice(0, -1) : token;
  });
}

assert.equal(manifest.length, 10, "suite must contain exactly ten tasks");
assert.equal(new Set(manifest.map((task) => task.id)).size, 10, "task ids must be unique");
assert.ok(fs.existsSync(go), `Go executable is missing: ${go}`);
const baselineHash = treeHash(seedRoot);

for (const task of manifest) {
  assert.ok(task.id && task.name && task.source, `${task.id || "task"}: incomplete identity`);
  assert.ok(Array.isArray(task.named_paths) && task.named_paths.length > 0, `${task.id}: named_paths is empty`);

  const oracle = path.join(suiteRoot, task.oracle);
  const diff = fs.readFileSync(oracle, "utf8");
  const changed = [...diff.matchAll(/^\+\+\+ b\/(.+)$/gm)].map((match) => match[1]);
  assert.deepEqual(changed, task.named_paths, `${task.id}: oracle must touch exactly named_paths`);
  assert.ok(!changed.some((relative) => /(^|\/)(test|tests|verifier)(\/|$)/i.test(relative)), `${task.id}: oracle edits a test or verifier`);

  const terse = fs.readFileSync(path.join(suiteRoot, "briefs", task.id, "terse.txt"), "utf8");
  const prose = fs.readFileSync(path.join(suiteRoot, "briefs", task.id, "prose.txt"), "utf8");
  const terseFacts = Object.fromEntries(terse.trim().split(/\r?\n/).map((line) => {
    const split = line.indexOf(":");
    assert.ok(split > 0, `${task.id}: malformed terse brief line`);
    return [line.slice(0, split), line.slice(split + 1).trim()];
  }));
  assert.deepEqual(Object.keys(terseFacts), ["intent", "do", "accept", "not", "after"], `${task.id}: terse fact labels changed`);
  const proseTokens = new Set(factTokens(prose));
  for (const fact of Object.values(terseFacts)) {
    const missing = factTokens(fact).filter((token) => !proseTokens.has(token));
    assert.deepEqual(missing, [], `${task.id}: prose omits terse fact tokens from: ${fact}`);
  }

  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), `agentb-comparative-${task.id}-`));
  const worktree = path.join(temporary, "worktree");
  try {
    fs.cpSync(seedRoot, worktree, { recursive: true });
    assert.equal(treeHash(worktree), baselineHash, `${task.id}: seed copy is not byte-identical`);
    const before = runVerifier(task, worktree);
    assert.notEqual(before.status, 0, `${task.id}: seeded verifier must fail`);

    const apply = spawnSync("git", ["apply", "--unidiff-zero", "--whitespace=nowarn", oracle], { cwd: worktree, encoding: "utf8", windowsHide: true });
    assert.equal(apply.status, 0, `${task.id}: oracle did not apply\n${apply.stderr}`);
    const after = runVerifier(task, worktree);
    assert.equal(after.status, 0, `${task.id}: oracle verifier failed\n${after.stdout}\n${after.stderr}`);
  } finally {
    fs.rmSync(temporary, { recursive: true, force: true });
  }
  process.stdout.write(`PASS ${task.id}\n`);
}

process.stdout.write(`PASS comparative fixture proof (${manifest.length} tasks, seed ${baselineHash})\n`);
