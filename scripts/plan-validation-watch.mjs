#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import {
  exactInputIdentity,
  validateProposal,
  validateResume,
} from "./plan-lint.mjs";

const scriptRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const requestSuffix = ".request.json";
const resultSuffix = ".result.json";

function bindingFor(requestBytes) {
  const bytes = Buffer.isBuffer(requestBytes) ? requestBytes : Buffer.from(String(requestBytes), "utf8");
  return {
    sha256: exactInputIdentity(bytes),
    bytes: bytes.length,
    text: bytes.toString("utf8"),
  };
}

function failure(binding, message, field = "request", expected = "valid JSON") {
  return {
    version: 1,
    status: "fail",
    operation: null,
    request: binding,
    errors: [{ message, field, expected }],
  };
}

function refusal(binding, operation, messages) {
  return {
    version: 1,
    status: "refused",
    operation,
    request: binding,
    errors: messages.map((message) => ({
      message,
      field: "boundary",
      expected: "validation only: validateProposal or validateResume",
    })),
  };
}

function ownKeys(value) {
  return value && typeof value === "object" && !Array.isArray(value) ? Object.keys(value) : [];
}

function validateUnpublishedProposal(payload) {
  const initial = validateProposal(payload);
  const current = String(payload.planText ?? "").match(/^## Index\s*$[\s\S]*$/m);
  if (!current || !initial.errors.includes("PLAN.md index: stale or malformed; run node scripts/plan-lint.mjs --write-index --structural")) return initial;
  const planText = `${payload.planText.slice(0, current.index)}${initial.indexSection}`;
  const validation = validateProposal({ ...payload, planText });
  // The validator's identity remains bound to the proposal bytes supplied by
  // the planner, not to the generated in-memory index used for this check.
  validation.proposalId = initial.proposalId;
  return validation;
}

/** Validate one exact request file payload without performing any requested side effect. */
export function validateDroppedRequest(requestBytes) {
  const binding = bindingFor(requestBytes);
  const requestText = binding.text;
  let request;
  try {
    request = JSON.parse(requestText);
  } catch (error) {
    return failure(binding, `request JSON is invalid: ${error.message}`);
  }
  if (!request || typeof request !== "object" || Array.isArray(request)) {
    return failure(binding, "request must be a JSON object", "request", "a JSON object");
  }

  const operation = typeof request.operation === "string" ? request.operation : null;
  const payloadKey = operation === "validateProposal" ? "proposal"
    : operation === "validateResume" ? "resume" : null;
  const allowed = new Set(["version", "operation", ...(payloadKey ? [payloadKey] : [])]);
  const sideEffects = ownKeys(request).filter((key) => !allowed.has(key));
  if (!payloadKey) sideEffects.unshift(`operation:${operation ?? "missing"}`);
  if (sideEffects.length) {
    return refusal(binding, operation, sideEffects.map((key) => `request field ${JSON.stringify(key)} is outside the validation-only boundary`));
  }
  if (request.version !== 1) {
    return failure(binding, `request version ${JSON.stringify(request.version)} is unsupported`, "version", "1");
  }
  const payload = request[payloadKey];
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
    return failure(binding, `${payloadKey} must be a JSON object`, payloadKey, "the existing validator input object");
  }

  try {
    const validation = operation === "validateProposal"
      ? validateUnpublishedProposal(payload)
      : validateResume(payload);
    const passed = operation === "validateProposal" ? validation.errors.length === 0 : validation.accepted;
    return {
      version: 1,
      status: passed ? "pass" : "fail",
      operation,
      request: binding,
      validation,
    };
  } catch (error) {
    return failure(binding, `validator rejected the request shape: ${error.message}`, payloadKey, "the existing validator input object");
  }
}

export function resultPathFor(requestPath) {
  if (!String(requestPath).endsWith(requestSuffix)) throw new TypeError(`request path must end with ${requestSuffix}`);
  return `${String(requestPath).slice(0, -requestSuffix.length)}${resultSuffix}`;
}

/** Read a result as absent, stale, or current for the request bytes on disk. */
export function readValidationResult(requestPath) {
  const resultPath = resultPathFor(requestPath);
  if (!fs.existsSync(resultPath)) return { state: "absent", result: null };
  const requestBytes = fs.readFileSync(requestPath);
  let result;
  try {
    result = JSON.parse(fs.readFileSync(resultPath, "utf8"));
  } catch (error) {
    return { state: "stale", result: null, error: `result JSON is invalid: ${error.message}` };
  }
  const current = bindingFor(requestBytes);
  if (result?.request?.sha256 !== current.sha256
      || result?.request?.bytes !== current.bytes
      || result?.request?.text !== current.text) {
    return { state: "stale", result };
  }
  return { state: result.status, result };
}

/** Process one request path and write only its adjacent result path. */
export function processRequestFile(requestPath) {
  const requestBytes = fs.readFileSync(requestPath);
  const result = validateDroppedRequest(requestBytes);
  const resultPath = resultPathFor(requestPath);
  fs.writeFileSync(resultPath, `${JSON.stringify(result, null, 2)}\n`, { encoding: "utf8", flag: "w" });
  return { resultPath, result };
}

function requestFiles(dropDirectory) {
  return fs.readdirSync(dropDirectory, { withFileTypes: true })
    .filter((entry) => entry.isFile() && entry.name.endsWith(requestSuffix))
    .map((entry) => path.join(dropDirectory, entry.name));
}

/** Start the validation-only resident watcher. */
export function startValidationWatcher({ dropDirectory = path.join(scriptRoot, "plan", "validation") } = {}) {
  const absoluteDrop = path.resolve(dropDirectory);
  fs.mkdirSync(absoluteDrop, { recursive: true });
  const seen = new Map();
  const pending = new Map();

  const processPath = (requestPath) => {
    let bytes;
    try { bytes = fs.readFileSync(requestPath); }
    catch (error) {
      if (error.code === "ENOENT") return;
      throw error;
    }
    const identity = exactInputIdentity(bytes);
    if (seen.get(requestPath) === identity) return;
    processRequestFile(requestPath);
    seen.set(requestPath, identity);
  };
  const schedule = (requestPath) => {
    clearTimeout(pending.get(requestPath));
    pending.set(requestPath, setTimeout(() => {
      pending.delete(requestPath);
      try { processPath(requestPath); }
      catch (error) { console.error(`plan validation watcher: ${error.message}`); }
    }, 25));
  };
  for (const requestPath of requestFiles(absoluteDrop)) schedule(requestPath);
  const watcher = fs.watch(absoluteDrop, (_event, name) => {
    if (name && String(name).endsWith(requestSuffix)) schedule(path.join(absoluteDrop, String(name)));
    else if (!name) for (const requestPath of requestFiles(absoluteDrop)) schedule(requestPath);
  });
  return {
    dropDirectory: absoluteDrop,
    close() {
      watcher.close();
      for (const timer of pending.values()) clearTimeout(timer);
      pending.clear();
    },
  };
}

function parseCLI(argv) {
  let dropDirectory = path.join(scriptRoot, "plan", "validation");
  for (let index = 0; index < argv.length; index += 1) {
    if (argv[index] === "--drop-dir" && argv[index + 1]) dropDirectory = path.resolve(argv[++index]);
    else throw new TypeError(`unknown argument: ${argv[index]}`);
  }
  return { dropDirectory };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const resident = startValidationWatcher(parseCLI(process.argv.slice(2)));
    console.log(`plan validation watcher ready: ${resident.dropDirectory}`);
    const stop = () => { resident.close(); process.exit(0); };
    process.once("SIGINT", stop);
    process.once("SIGTERM", stop);
  } catch (error) {
    console.error(`plan validation watcher failed: ${error.message}`);
    process.exit(2);
  }
}
