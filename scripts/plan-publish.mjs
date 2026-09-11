#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import { exactInputIdentity, loadPublishedProposal, validateProposal } from "./plan-lint.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const defaultRoot = path.resolve(here, "..");
const manifestName = ".plan-publication.json";
const staleIndexError = "PLAN.md index: stale or malformed; run node scripts/plan-lint.mjs --write-index --structural";

function normalize(relative) {
  return String(relative).replaceAll("\\", "/");
}

function sha256(value) {
  return crypto.createHash("sha256").update(value).digest("hex");
}

function readCandidate(candidate) {
  const planPath = path.join(candidate, "PLAN.md");
  if (!fs.existsSync(planPath)) throw new Error("candidate is missing PLAN.md");
  const files = [{ relative: "PLAN.md", text: fs.readFileSync(planPath, "utf8") }];
  for (const label of ["items", "archive"]) {
    const directory = path.join(candidate, "plan", label);
    if (!fs.existsSync(directory)) continue;
    for (const name of fs.readdirSync(directory).sort()) {
      const full = path.join(directory, name);
      if (!fs.statSync(full).isFile() || !name.endsWith(".md")) throw new Error(`candidate contains unsupported entry plan/${label}/${name}`);
      files.push({ relative: `plan/${label}/${name}`, text: fs.readFileSync(full, "utf8") });
    }
  }
  return files;
}

function mergedProposal(root, candidateFiles) {
  const published = loadPublishedProposal(root);
  const overrides = new Map(candidateFiles.filter(({ relative }) => relative !== "PLAN.md").map(({ relative, text }) => [normalize(relative), text]));
  const items = new Map(published.itemContents.map(({ relative, text }) => [normalize(relative), text]));
  for (const [relative, text] of overrides) items.set(relative, text);
  return {
    planText: candidateFiles.find(({ relative }) => relative === "PLAN.md").text,
    itemContents: [...items].map(([relative, text]) => ({ relative, text })),
    inputErrors: published.inputErrors,
  };
}

function publicationErrors(planText) {
  const count = [...String(planText).matchAll(/^## Current work order\b/gm)].length;
  return count === 1 ? [] : [`PUBLICATION: found ${count} Current work order bodies; expected exactly one`];
}

function indexedPlan(planText, indexSection) {
  const newline = planText.includes("\r\n") ? "\r\n" : "\n";
  const replacement = indexSection.replaceAll("\n", newline);
  if (!/^## Index\s*$/m.test(planText)) throw new Error("candidate PLAN.md has no ## Index section");
  return planText.replace(/^## Index\s*$[\s\S]*$/m, replacement);
}

function writeExact(full, text) {
  fs.mkdirSync(path.dirname(full), { recursive: true });
  const temporary = `${full}.tmp-${process.pid}-${crypto.randomBytes(6).toString("hex")}`;
  fs.writeFileSync(temporary, text, { encoding: "utf8", flag: "wx" });
  fs.renameSync(temporary, full);
}

export function workerStopped(planText) {
  const orderId = String(planText).match(/^Order ID:\s*`([^`]+)`/m)?.[1];
  if (!orderId) return { stopped: false, reason: "published plan has no Order ID" };
  const inFlight = String(planText).match(/^## In flight\s*$\n([\s\S]*?)(?=^## |(?![\s\S]))/m)?.[1] ?? "";
  const escaped = orderId.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const active = new Set();
  const markers = new RegExp(`^(?:-\\s*)?${escaped}/(W\\d+)\\s+(started|completed)\\b`, "gmi");
  for (const match of inFlight.matchAll(markers)) {
    if (match[2].toLowerCase() === "started") active.add(match[1].toUpperCase());
    else active.delete(match[1].toUpperCase());
  }
  return active.size ? { stopped: false, reason: `worker is active in ${[...active].join(", ")}` } : { stopped: true, reason: "no started-without-completion marker" };
}

function validationFailure(errors) {
  const error = new Error(`publication validation failed:\n${errors.map((value) => `- ${value}`).join("\n")}`);
  error.code = "VALIDATION_FAILED";
  return error;
}

export function preparePublication({ root = defaultRoot, candidate }) {
  root = path.resolve(root);
  candidate = path.resolve(candidate);
  if (root === candidate) throw new Error("candidate must be outside active plan state");
  const candidateFiles = readCandidate(candidate);
  const proposed = mergedProposal(root, candidateFiles);
  const extraErrors = publicationErrors(proposed.planText);
  const first = validateProposal({ ...proposed, structuralOnly: true, inputErrors: [...proposed.inputErrors, ...extraErrors] });
  const nonIndexErrors = first.errors.filter((message) => message !== staleIndexError);
  if (nonIndexErrors.length) throw validationFailure(nonIndexErrors);

  const generatedPlan = indexedPlan(proposed.planText, first.indexSection);
  const generatedFiles = candidateFiles.map((entry) => entry.relative === "PLAN.md" ? { ...entry, text: generatedPlan } : entry);
  const combined = mergedProposal(root, generatedFiles);
  const result = validateProposal({ ...combined, inputErrors: [...combined.inputErrors, ...publicationErrors(generatedPlan)] });
  if (result.errors.length) throw validationFailure(result.errors);

  writeExact(path.join(candidate, "PLAN.md"), generatedPlan);
  const base = loadPublishedProposal(root);
  const baseResult = validateProposal(base);
  const files = generatedFiles.map(({ relative, text }) => ({ relative, sha256: sha256(Buffer.from(text, "utf8")), bytes: Buffer.byteLength(text, "utf8") }));
  const manifest = {
    schema: 1,
    prepared_at: new Date().toISOString(),
    base_id: baseResult.proposalId,
    proposal_id: result.proposalId,
    files,
  };
  writeExact(path.join(candidate, manifestName), `${JSON.stringify(manifest, null, 2)}\n`);
  return { manifest, itemCount: result.itemCount, warnings: result.warnings };
}

function readSealedCandidate(candidate, manifest) {
  return manifest.files.map((record) => {
    const relative = normalize(record.relative);
    if (relative !== "PLAN.md" && !/^plan\/(?:items|archive)\/[^/]+\.md$/.test(relative)) throw new Error(`manifest contains unsupported path ${relative}`);
    const full = path.join(candidate, ...relative.split("/"));
    const bytes = fs.readFileSync(full);
    if (bytes.length !== record.bytes || sha256(bytes) !== record.sha256) throw new Error(`sealed candidate changed after validation: ${relative}`);
    return { relative, text: bytes.toString("utf8") };
  });
}

function publishTransaction(root, files) {
  const nonce = `${process.pid}-${crypto.randomBytes(6).toString("hex")}`;
  const records = files.map(({ relative, text }) => {
    const target = path.join(root, ...relative.split("/"));
    fs.mkdirSync(path.dirname(target), { recursive: true });
    const temporary = `${target}.publish-${nonce}`;
    const backup = `${target}.backup-${nonce}`;
    fs.writeFileSync(temporary, text, { encoding: "utf8", flag: "wx" });
    return { target, temporary, backup, existed: fs.existsSync(target), installed: false };
  });
  try {
    for (const record of records) {
      if (record.existed) fs.renameSync(record.target, record.backup);
      fs.renameSync(record.temporary, record.target);
      record.installed = true;
    }
  } catch (error) {
    for (const record of records.reverse()) {
      try { if (record.installed && fs.existsSync(record.target)) fs.rmSync(record.target, { force: true }); } catch {}
      try { if (record.existed && fs.existsSync(record.backup)) fs.renameSync(record.backup, record.target); } catch {}
      try { if (fs.existsSync(record.temporary)) fs.rmSync(record.temporary, { force: true }); } catch {}
    }
    throw error;
  }
  for (const record of records) if (record.existed && fs.existsSync(record.backup)) fs.rmSync(record.backup, { force: true });
}

export function publishPublication({ root = defaultRoot, candidate }) {
  root = path.resolve(root);
  candidate = path.resolve(candidate);
  const manifest = JSON.parse(fs.readFileSync(path.join(candidate, manifestName), "utf8"));
  if (manifest.schema !== 1 || !Array.isArray(manifest.files)) throw new Error("invalid publication manifest");
  const current = loadPublishedProposal(root);
  const stopped = workerStopped(current.planText);
  if (!stopped.stopped) throw new Error(`publication refused: ${stopped.reason}`);
  const currentResult = validateProposal(current);
  if (currentResult.proposalId !== manifest.base_id) throw new Error("publication refused: active plan state changed after preparation");

  const files = readSealedCandidate(candidate, manifest);
  const combined = mergedProposal(root, files);
  const result = validateProposal({ ...combined, inputErrors: [...combined.inputErrors, ...publicationErrors(combined.planText)] });
  if (result.errors.length) throw validationFailure(result.errors);
  if (result.proposalId !== manifest.proposal_id) throw new Error("publication refused: validated proposal identity does not match the seal");
  publishTransaction(root, files);
  return { proposalId: result.proposalId, files: files.map(({ relative }) => relative), warnings: result.warnings };
}

function parseCLI(argv) {
  const command = argv[2];
  let root = defaultRoot;
  let candidate = "";
  for (let index = 3; index < argv.length; index += 1) {
    if (argv[index] === "--root" && argv[index + 1]) root = argv[++index];
    else if (argv[index] === "--candidate" && argv[index + 1]) candidate = argv[++index];
    else throw new Error(`unknown argument: ${argv[index]}`);
  }
  if (!candidate) throw new Error("--candidate is required");
  return { command, root, candidate };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const { command, root, candidate } = parseCLI(process.argv);
    const result = command === "prepare" ? preparePublication({ root, candidate })
      : command === "publish" ? publishPublication({ root, candidate })
        : (() => { throw new Error("command must be prepare or publish"); })();
    process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
  } catch (error) {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  }
}
