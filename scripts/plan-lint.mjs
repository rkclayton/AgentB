#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const scriptRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const validStates = new Set(["proposed", "live", "shipped", "superseded", "dead"]);
const validKinds = new Set(["defect", "feature", "decision", "discovery"]);
const validSurfaces = new Set(["chat", "chat-list", "composer", "tab-strip", "console", "settings", "run-loop", "accounting", "tools", "install", "plan", "tests"]);
const validAuthorizations = new Set(["operator"]);
const validMetadata = new Set(["state", "milestone", "shipped", "kind", "surfaces", "authorization", "evidence", "acceptance", "depends-on", "agent"]);

function lineCount(text) {
  return text === "" ? 0 : text.split(/\r?\n/).length - (text.endsWith("\n") ? 1 : 0);
}

function parseMetadata(text, relative, errors) {
  const lines = text.split(/\r?\n/);
  const fenced = lines[0] === "---";
  const end = fenced ? lines.indexOf("---", 1) : lines.indexOf("");
  if (end < 0) {
    errors.push(`${relative}: invalid frontmatter shape`);
    return null;
  }
  const start = fenced ? 1 : 0;
  const metadata = new Map();
  let current = null;
  for (const line of lines.slice(start, end)) {
    const field = line.match(/^([a-z][a-z-]*):(?:\s(.*))?$/);
    if (field) {
      current = field[1];
      if (!validMetadata.has(current)) errors.push(`${relative}: unknown metadata field ${JSON.stringify(current)}`);
      if (metadata.has(current)) errors.push(`${relative}: duplicate metadata field ${JSON.stringify(current)}`);
      metadata.set(current, (field[2] ?? "").trim());
    } else if (/^\s+\S/.test(line) && current) {
      metadata.set(current, `${metadata.get(current)} ${line.trim()}`.trim());
    } else {
      errors.push(`${relative}: invalid frontmatter line ${JSON.stringify(line)}`);
    }
  }
  const bodyStartLine = fenced ? end + 1 : end;
  return { metadata, body: lines.slice(bodyStartLine).join("\n") };
}

function normalizeItemContents(itemContents) {
  const entries = Array.isArray(itemContents)
    ? itemContents
    : Object.entries(itemContents ?? {}).map(([relative, text]) => ({ relative, text }));
  return entries.map(({ relative, text }) => ({
    relative: String(relative).replaceAll("\\", "/"),
    text: String(text),
  })).sort((a, b) => a.relative.localeCompare(b.relative));
}

function replaceCurrentOrderBody(planText, orderBody) {
  if (orderBody === undefined || orderBody === null) return planText;
  const current = planText.match(/^## Current work order[^\n]*\n[\s\S]*?(?=^## (?:Next work order|In flight|Index)|(?![\s\S]))/m);
  if (!current) return planText;
  const heading = current[0].match(/^## Current work order[^\n]*/)?.[0] ?? "## Current work order";
  const replacement = `${heading}\n\n${String(orderBody).replace(/^\s+|\s+$/g, "")}\n\n`;
  return `${planText.slice(0, current.index)}${replacement}${planText.slice(current.index + current[0].length)}`;
}

function proposalIdentity(planText, orderBody, itemContents) {
  const exact = JSON.stringify({ planText, orderBody: orderBody ?? null, itemContents: normalizeItemContents(itemContents) });
  return exactInputIdentity(exact);
}

/** Bind a result to exact UTF-8 input bytes. */
export function exactInputIdentity(text) {
  const bytes = Buffer.isBuffer(text) ? text : Buffer.from(String(text), "utf8");
  return `sha256:${crypto.createHash("sha256").update(bytes).digest("hex")}`;
}

function issueDetail(message) {
  const metadata = message.match(/(?:metadata field |metadata |unresolved |invalid )([a-z][a-z-]*)/i)?.[1];
  const reference = message.match(/(?:reference |item )((?:\[\[)?[0-9]+[a-z]*(?:\]\])?)/i)?.[1];
  const field = metadata ?? (message.includes("frontmatter") ? "frontmatter"
    : message.includes("heading") ? "heading"
      : message.includes("item id") || message.includes("item IDs") ? "item-id"
        : message.includes("In flight marker") ? "in-flight-marker"
          : message.includes("Current work order") ? "current-work-order"
            : message.includes("Index") || message.includes("index") ? "index"
              : reference ? "reference" : "plan");
  const expected = message.includes("no recorded authorization") ? "authorization: operator in item metadata"
    : message.includes("invalid authorization") ? "operator"
    : message.includes("non-empty ## Unresolved") ? "(none), or an explicit discovery/blocker representation"
      : message.includes("does not exist") || message.includes("unresolved item reference") ? "a resolvable item file in plan/items or plan/archive"
        : message.includes("expected live") ? "state: live"
          : message.includes("invalid milestone") ? "unknown, -, or 0.<number>"
            : message.includes("invalid state") ? "proposed, live, shipped, superseded, or dead"
              : message.includes("invalid kind") ? "defect, feature, decision, or discovery"
                : message.includes("invalid surfaces") ? "one or more registered surface names"
                  : message.includes("missing required metadata") ? "a non-empty required frontmatter field"
                    : message.includes("missing top-level item heading") ? "# <item-id> — <title>"
                      : message.includes("missing ## Unresolved") ? "a terminal ## Unresolved section"
                        : message.includes("belongs to another order") ? "the current Order ID before /W<n>"
                          : message.includes("stale or malformed") ? "the generated index from --write-index --structural"
                            : "the form named by the validation message";
  return { message, field: reference ?? field, expected };
}

function escapeCell(value) {
  return String(value).replaceAll("|", "\\|").replaceAll("\n", " ");
}

function unresolvedEntries(value) {
  if (value === null || value === "(none)") return [];
  const lines = value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
  const entries = [];
  for (const line of lines) {
    const explicit = line.match(/^(?:-\s*)?\[(discovery|blocker)\]\s+(.+)$/i);
    entries.push(explicit
      ? { category: explicit[1].toLowerCase(), text: explicit[2] }
      : { category: "legacy", text: line });
  }
  return entries;
}

/** Validate an exact plan proposal without writing it to the repository. */
export function validateProposal({ planText, orderBody = null, itemContents, structuralOnly = false, inputErrors = [] }) {
  const errors = [...inputErrors];
  const warnings = [];
  const admission = { errors: [], warnings: [] };
  const blockers = [];
  const completion = [];
  const items = new Map();
  const sourceTexts = [];
  const normalizedItems = normalizeItemContents(itemContents);

  for (const { relative, text } of normalizedItems) {
    if (relative === "plan/_reference.md" || relative === "plan/_history.md") {
      sourceTexts.push({ relative, text });
      continue;
    }
    const location = relative.startsWith("plan/archive/") ? "archive"
      : relative.startsWith("plan/items/") ? "items" : null;
    if (!location || !relative.endsWith(".md")) {
      errors.push(`${relative}: expected an item path under plan/items or plan/archive ending in .md`);
      continue;
    }
    const id = path.posix.basename(relative, ".md");
    sourceTexts.push({ relative, text });
    const parsed = parseMetadata(text, relative, errors);
    if (!parsed) continue;
    const { metadata, body } = parsed;
    const state = metadata.get("state");
    const milestone = metadata.get("milestone");
    const firstKeys = [...metadata.keys()];
    if (firstKeys[0] !== "state" || firstKeys[1] !== "milestone") errors.push(`${relative}: state and milestone must be the first two metadata fields`);
    if (!validStates.has(state)) errors.push(`${relative}: invalid state ${JSON.stringify(state)}`);
    if (!/^(?:unknown|-|0\.\d+)$/.test(milestone ?? "")) errors.push(`${relative}: invalid milestone ${JSON.stringify(milestone)}`);
    if (location === "items" && !["proposed", "live"].includes(state)) errors.push(`${relative}: state ${state} must not live in plan/items`);
    if (location === "archive" && ["proposed", "live"].includes(state)) errors.push(`${relative}: state ${state} must not live in plan/archive`);
    const required = state === "live" ? ["state", "milestone", "kind", "surfaces", "evidence", "acceptance"]
      : state === "proposed" ? ["state", "milestone", "kind", "surfaces", "evidence"] : ["state", "milestone"];
    for (const field of required) if (!metadata.has(field) || metadata.get(field) === "") errors.push(`${relative}: missing required metadata ${field}`);
    const kind = metadata.get("kind");
    if (kind && kind !== "unknown" && !validKinds.has(kind)) errors.push(`${relative}: invalid kind ${JSON.stringify(kind)}`);
    const rawSurfaces = metadata.get("surfaces");
    const surfaces = rawSurfaces?.split(",").map((value) => value.trim()).filter(Boolean) ?? [];
    if (rawSurfaces && rawSurfaces !== "unknown" && (!surfaces.length || surfaces.some((surface) => !validSurfaces.has(surface)))) errors.push(`${relative}: invalid surfaces ${JSON.stringify(rawSurfaces)}`);
    const authorization = metadata.get("authorization");
    if (authorization && authorization !== "unknown" && !validAuthorizations.has(authorization)) errors.push(`${relative}: invalid authorization ${JSON.stringify(authorization)}`);
    for (const field of ["milestone", "kind", "surfaces", "evidence", "acceptance"]) if (metadata.get(field) === "unknown") warnings.push(`${relative}: unresolved metadata ${field}`);
    const heading = body.match(/^# ([0-9]+[a-z]*) — ([^\n]+)$/m);
    if (!heading) {
      errors.push(`${relative}: missing top-level item heading`);
      continue;
    }
    if (heading[1] !== id) errors.push(`${relative}: heading id ${heading[1]} does not match filename ${id}`);
    if (items.has(id)) {
      errors.push(`${relative}: duplicate item id ${id}`);
      continue;
    }
    const unresolved = body.match(/\n## Unresolved\n\n([\s\S]*)$/);
    if (!unresolved) errors.push(`${relative}: missing ## Unresolved`);
    if (state === "shipped" && !/\bv\d+\.\d+\.\d+\b|\b[0-9a-f]{7,40}\b/i.test(text)) warnings.push(`${relative}: shipped item names no tag or commit`);
    const lines = lineCount(text);
    if (lines > 100) warnings.push(`${relative}: long item (${lines} lines)`);
    const unresolvedText = unresolved ? unresolved[1].trim() : null;
    items.set(id, { id, state, milestone, kind: kind ?? "", surfaces, rawSurfaces: rawSurfaces ?? "", relative, title: heading[2], lines, unresolved: unresolvedText, unresolvedEntries: unresolvedEntries(unresolvedText), metadata });
  }

  for (const { relative, text } of sourceTexts) {
    for (const match of text.matchAll(/\[\[([0-9]+[a-z]*)\]\]/g)) if (!items.has(match[1])) errors.push(`${relative}: unresolved item reference [[${match[1]}]]`);
  }
  const sortedItems = [...items.values()].sort((a, b) => a.state.localeCompare(b.state) || a.milestone.localeCompare(b.milestone, "en", { numeric: true }) || a.id.localeCompare(b.id, "en", { numeric: true }));
  const indexSection = [
    "## Index", "", "Generated from item files. This is navigation, not an execution priority queue.", "",
    "| State | Kind | Milestone | Item | Title | Surfaces | Lines |",
    "| --- | --- | --- | --- | --- | --- | ---: |",
    ...sortedItems.map((item) => `| ${item.state} | ${item.kind || "-"} | ${item.milestone} | [${item.id}](${item.relative}) | ${escapeCell(item.title)} | ${escapeCell(item.rawSurfaces || "-")} | ${item.lines} |`), "",
  ].join("\n");

  const effectivePlan = replaceCurrentOrderBody(String(planText ?? ""), orderBody);
  if (!inputErrors.includes("missing PLAN.md")) {
  for (const match of effectivePlan.matchAll(/\[\[([0-9]+[a-z]*)\]\]/g)) if (!items.has(match[1])) errors.push(`PLAN.md: unresolved item reference [[${match[1]}]]`);
  const indexMatch = effectivePlan.match(/^## Index\s*$[\s\S]*$/m);
  if (!indexMatch) errors.push("PLAN.md: missing ## Index");
  else if (indexMatch[0].replaceAll("\r\n", "\n").replace(/\s+$/, "") !== indexSection.replace(/\s+$/, "")) errors.push("PLAN.md index: stale or malformed; run node scripts/plan-lint.mjs --write-index --structural");
  if (/^## (?:Completed|Closed|Previous) work order\b/im.test(effectivePlan)) errors.push("PLAN.md: completed-order heading is not allowed");
  const currentMatch = effectivePlan.match(/^## Current work order([^\n]*)\n([\s\S]*?)(?=^## (?:Next work order|In flight|Index)|(?![\s\S]))/m);
  if (!currentMatch) errors.push("PLAN.md: cannot find Current work order");
  else {
    const currentText = currentMatch[2];
    const orderId = currentText.match(/^Order ID:\s*`([^`]+)`/m)?.[1] ?? currentMatch[1].match(/\b(v\d+\.\d+\.\d+|[A-Z][A-Z0-9-]+)\b/)?.[1];
    const inFlight = effectivePlan.match(/^## In flight\s*$\n([\s\S]*?)(?=^## |(?![\s\S]))/m)?.[1] ?? "";
    if (orderId) {
      const activeMarkers = new Map();
      for (const marker of inFlight.matchAll(/^(?:-\s*)?`?([^\s`/]+)\/(W\d+)\s+(started|completed)\b/gmi)) {
        const key = `${marker[1]}/${marker[2].toUpperCase()}`;
        if (marker[3].toLowerCase() === "started") activeMarkers.set(key, { orderId: marker[1], workId: marker[2].toUpperCase() });
        else activeMarkers.delete(key);
      }
      for (const marker of activeMarkers.values()) if (marker.orderId !== orderId) errors.push(`PLAN.md: In flight marker ${marker.orderId}/${marker.workId} belongs to another order (current ${orderId})`);
    }
    const workItems = [...currentText.matchAll(/^- (W\d+)\s+\*\*(?:item\s+)?([0-9]+[a-z]*)\b([^\n]*)/gmi)];
    if (!/No product changes/i.test(currentText)) {
      const executable = new Map();
      for (const match of workItems) {
        const clauseStart = match.index;
        const after = currentText.slice(clauseStart + 1);
        const next = after.search(/\n- W\d+\s+/);
        const workId = match[1].toUpperCase();
        const id = match[2].toLowerCase();
        const clause = next < 0 ? currentText.slice(clauseStart) : currentText.slice(clauseStart, clauseStart + 1 + next);
        const entry = executable.get(id) ?? { clause: "", workIds: [] };
        entry.clause += `\n${clause}`;
        entry.workIds.push(workId);
        executable.set(id, entry);
      }
      if (!structuralOnly && !executable.size) {
        const message = "ORDER GATE: no executable item IDs found in W headings";
        errors.push(message);
        admission.errors.push(message);
      }
      for (const [id, execution] of executable) {
        const item = items.get(id);
        if (!item) {
          if (!structuralOnly) {
            const message = `ORDER GATE: executable item ${id} does not exist`;
            errors.push(message);
            admission.errors.push(message);
          }
          continue;
        }
        const completedWork = execution.workIds.filter((workId) => new RegExp(`^(?:-\\s*)?${orderId?.replace(/[.*+?^${}()|[\\]\\]/g, "\\$&")}/${workId} completed\\b`, "mi").test(inFlight));
        const acceptanceEvidence = item.state === "shipped"
          && item.relative.startsWith("plan/archive/")
          && Boolean(item.metadata.get("shipped"))
          && !["", "unknown"].includes(item.metadata.get("evidence") ?? "")
          && !["", "unknown"].includes(item.metadata.get("acceptance") ?? "");
        const implementationComplete = execution.workIds.every((workId) => completedWork.includes(workId));
        completion.push({
          itemId: id,
          workIds: execution.workIds,
          completedWork,
          implementationComplete,
          acceptanceEvidence,
          status: acceptanceEvidence && implementationComplete ? "complete" : acceptanceEvidence || completedWork.length ? "partial" : "pending",
        });
        if (acceptanceEvidence && !implementationComplete) {
          const missing = execution.workIds.filter((workId) => !completedWork.includes(workId));
          errors.push(`RECONCILE: archived shipped item ${id} is missing completed implementation marker(s): ${missing.join(", ")}`);
        }
        if (structuralOnly) continue;
        if (item.state !== "live") {
          const message = `ORDER GATE: executable item ${id} is ${item.state}, expected live`;
          errors.push(message);
          admission.errors.push(message);
        }
        for (const field of ["milestone", "kind", "surfaces", "evidence", "acceptance"]) {
          const value = item.metadata.get(field);
          if (!value || value === "unknown" || (field === "milestone" && value === "-")) {
            const message = `ORDER GATE: executable item ${id} has unresolved ${field}`;
            errors.push(message);
            admission.errors.push(message);
          }
        }
        if (item.metadata.get("authorization") !== "operator") {
          const message = `ORDER GATE: executable item ${id} has no recorded authorization for this scope`;
          errors.push(message);
          admission.errors.push(message);
        }
        const discoveries = item.unresolvedEntries.filter(({ category }) => category === "discovery");
        const itemBlockers = item.unresolvedEntries.filter(({ category }) => category === "blocker");
        const legacy = item.unresolvedEntries.filter(({ category }) => category === "legacy");
        if (discoveries.length) {
          const message = `ORDER GATE: item ${id} unresolved entry is explicitly covered by discovery-first work`;
          warnings.push(message);
          admission.warnings.push(message);
        }
        for (const entry of itemBlockers) {
          const blocker = { itemId: id, reason: entry.text, message: `ORDER BLOCKER: item ${id}: ${entry.text}` };
          blockers.push(blocker);
          errors.push(blocker.message);
        }
        if (legacy.length) {
          const message = `ORDER GATE: executable item ${id} has non-empty ## Unresolved not covered by its W clause`;
          errors.push(message);
          admission.errors.push(message);
        }
      }
    }
  }
  }
  return {
    proposalId: proposalIdentity(String(planText ?? ""), orderBody, normalizedItems),
    errors, warnings, admission, blockers, completion,
    errorDetails: errors.map(issueDetail),
    warningDetails: warnings.map(issueDetail),
    itemCount: items.size,
    indexSection,
    effectivePlan,
  };
}

function currentOrderRecord(planText) {
  const match = String(planText ?? "").match(/^## Current work order([^\n]*)\n([\s\S]*?)(?=^## (?:Next work order|In flight|Index)|(?![\s\S]))/m);
  if (!match) return null;
  const text = match[2];
  const orderId = text.match(/^Order ID:\s*`([^`]+)`/m)?.[1] ?? match[1].match(/\b(v\d+\.\d+\.\d+|[A-Z][A-Z0-9-]+)\b/)?.[1] ?? null;
  const revision = text.match(/^\*\*Revision(?::)?\s+(r[0-9]+)\b/im)?.[1].toLowerCase() ?? null;
  const inFlight = String(planText ?? "").match(/^## In flight\s*$\n([\s\S]*?)(?=^## |(?![\s\S]))/m)?.[1] ?? "";
  const work = [...text.matchAll(/^- (W\d+)\s+\*\*(?:item\s+)?([0-9]+[a-z]*)\b/gmi)].map((entry) => ({ workId: entry[1].toUpperCase(), itemId: entry[2].toLowerCase() }));
  const completedWork = orderId ? [...inFlight.matchAll(new RegExp(`^(?:-\\s*)?${orderId.replace(/[.*+?^${}()|[\\]\\]/g, "\\$&")}/(W\\d+) completed\\b`, "gmi"))].map((entry) => entry[1].toUpperCase()) : [];
  return { orderId, revision, text, work, completedWork };
}

function hashText(text) {
  return crypto.createHash("sha256").update(String(text), "utf8").digest("hex");
}

/** Merge immutable snapshot parts. Later parts add or explicitly supersede covered inputs. */
export function mergeAcceptedSnapshotParts(parts) {
  if (!Array.isArray(parts) || !parts.length) throw new TypeError("accepted snapshot parts must be a non-empty array");
  let planText = null;
  const itemContents = new Map();
  const coverage = new Map();
  const records = [];
  for (const part of parts) {
    if (!/^r[0-9]+$/i.test(part.revision ?? "")) throw new TypeError("snapshot part revision must have form r<number>");
    if (!part.takenAt || Number.isNaN(Date.parse(part.takenAt))) throw new TypeError("snapshot part takenAt must be an ISO timestamp");
    const revision = part.revision.toLowerCase();
    if (part.planText !== undefined) {
      planText = String(part.planText);
      coverage.set("PLAN.md", { revision, takenAt: part.takenAt, sha256: hashText(planText) });
    }
    for (const entry of normalizeItemContents(part.itemContents)) {
      itemContents.set(entry.relative, entry.text);
      coverage.set(entry.relative, { revision, takenAt: part.takenAt, sha256: hashText(entry.text) });
    }
    records.push({ revision, takenAt: part.takenAt });
  }
  if (planText === null) throw new TypeError("accepted snapshot union must cover PLAN.md");
  return {
    revision: records.at(-1).revision,
    takenAt: records.at(-1).takenAt,
    planText,
    itemContents: [...itemContents].map(([relative, text]) => ({ relative, text })),
    coverage: Object.fromEntries(coverage),
    parts: records,
  };
}

function resumeInputs(proposal) {
  const order = currentOrderRecord(proposal.planText);
  const contents = new Map(normalizeItemContents(proposal.itemContents).map((entry) => [entry.relative, entry.text]));
  const required = new Map();
  if (order) {
    required.set("PLAN.md", order.text.replace(/\s+$/, ""));
    for (const { itemId } of order.work) {
      const path = [`plan/items/${itemId}.md`, `plan/archive/${itemId}.md`].find((candidate) => contents.has(candidate));
      if (path) required.set(path, contents.get(path));
      else required.set(`plan/items/${itemId}.md`, null);
    }
  }
  return { order, required };
}

/** Validate a resume against an immutable accepted-snapshot union and an explicit revision. */
export function validateResume({ acceptedParts, published, revision = null, deliveryEvidence = null }) {
  const accepted = mergeAcceptedSnapshotParts(acceptedParts);
  const acceptedInputs = resumeInputs(accepted);
  const publishedInputs = resumeInputs(published);
  const errors = [];
  if (!publishedInputs.order) errors.push("RESUME: published PLAN.md has no Current work order; expected one accepted order body");
  if (publishedInputs.order?.orderId && /-r[0-9]+$/i.test(publishedInputs.order.orderId)) errors.push("RESUME: revision is encoded in Order ID; expected revision identity outside the parsed order id");
  if (!publishedInputs.order?.revision) errors.push("RESUME: published brief has no revision identity; expected **Revision: r<number>** outside Order ID");

  const uncovered = [...publishedInputs.required.keys()].filter((relative) => !accepted.coverage[relative]);
  for (const relative of uncovered) errors.push(`RESUME: uncovered input ${relative}; expected it in the original snapshot or a dated amendment`);
  const changed = [];
  for (const [relative, text] of publishedInputs.required) {
    if (!accepted.coverage[relative] || text === null) continue;
    const acceptedText = acceptedInputs.required.get(relative);
    if (acceptedText === undefined || acceptedText === null || hashText(acceptedText) !== hashText(text)) changed.push(relative);
  }

  if (changed.length) {
    if (!revision) {
      errors.push(`RESUME: published brief differs from accepted snapshot at ${changed.join(", ")}; expected an explicit delivered revision naming changes and resume point`);
    } else {
      if (revision.from !== accepted.revision) errors.push(`RESUME: revision from is ${revision.from ?? "missing"}; expected ${accepted.revision}`);
      if (revision.to !== publishedInputs.order?.revision) errors.push(`RESUME: revision to is ${revision.to ?? "missing"}; expected ${publishedInputs.order?.revision ?? "the published revision"}`);
      if (!revision.summary?.trim()) errors.push("RESUME: revision summary is missing; expected what changed");
      if (!/^W[0-9]+$/i.test(revision.resumeAt ?? "")) errors.push("RESUME: revision resumeAt is missing; expected W<number>");
      if (revision.delivered !== true) errors.push("RESUME: revision is not recorded as delivered; expected delivered: true from the explicit revision channel");
      const named = new Set((revision.changedPaths ?? []).map((value) => String(value).replaceAll("\\", "/")));
      for (const relative of changed) if (!named.has(relative)) errors.push(`RESUME: changed input ${relative} is not named by the revision; expected it in changedPaths`);
    }
  }

  const resumeAt = revision?.resumeAt?.toUpperCase() ?? null;
  const completedWork = publishedInputs.order?.completedWork ?? [];
  if (resumeAt && completedWork.includes(resumeAt) && !(revision.reopen ?? []).map((value) => String(value).toUpperCase()).includes(resumeAt)) {
    errors.push(`RESUME: ${resumeAt} is already complete; expected a later resume point or explicit reopen entry`);
  }
  return {
    accepted: errors.length === 0,
    errors,
    acceptedRevision: accepted.revision,
    publishedRevision: publishedInputs.order?.revision ?? null,
    coverage: Object.keys(accepted.coverage).sort(),
    uncovered,
    changed,
    resumeAt,
    skipCompleted: completedWork,
    revisionDelivered: revision?.delivered === true,
    deliveryVerified: Boolean(deliveryEvidence?.verified && deliveryEvidence?.recordedAt),
  };
}

export function loadPublishedProposal(root = scriptRoot) {
  const planPath = path.join(root, "PLAN.md");
  const itemContents = [];
  const inputErrors = [];
  for (const label of ["items", "archive"]) {
    const directory = path.join(root, "plan", label);
    if (!fs.existsSync(directory)) {
      inputErrors.push(`missing directory: plan${path.sep}${label}`);
      continue;
    }
    for (const name of fs.readdirSync(directory).sort()) if (name.endsWith(".md")) itemContents.push({ relative: path.posix.join("plan", label, name), text: fs.readFileSync(path.join(directory, name), "utf8") });
  }
  for (const extra of ["plan/_reference.md", "plan/_history.md"]) {
    const full = path.join(root, ...extra.split("/"));
    if (fs.existsSync(full)) itemContents.push({ relative: extra, text: fs.readFileSync(full, "utf8") });
  }
  if (!fs.existsSync(planPath)) inputErrors.push("missing PLAN.md");
  return { planText: fs.existsSync(planPath) ? fs.readFileSync(planPath, "utf8") : "", itemContents, inputErrors };
}

function runCLI() {
  let root = scriptRoot;
  let structuralOnly = false;
  let writeIndex = false;
  for (let i = 2; i < process.argv.length; i += 1) {
    const arg = process.argv[i];
    if (arg === "--structural") structuralOnly = true;
    else if (arg === "--write-index") writeIndex = true;
    else if (arg === "--root" && process.argv[i + 1]) root = path.resolve(process.argv[++i]);
    else {
      console.error(`unknown argument: ${arg}`);
      process.exit(2);
    }
  }
  const loaded = loadPublishedProposal(root);
  let result = validateProposal({ ...loaded, structuralOnly });
  if (writeIndex) {
    const stale = "PLAN.md index: stale or malformed; run node scripts/plan-lint.mjs --write-index --structural";
    if (!result.errors.filter((message) => message !== stale).length) {
      const newline = loaded.planText.includes("\r\n") ? "\r\n" : "\n";
      fs.writeFileSync(path.join(root, "PLAN.md"), loaded.planText.replace(/^## Index\s*$[\s\S]*$/m, result.indexSection).replaceAll("\n", newline), "utf8");
      result = validateProposal({ ...loadPublishedProposal(root), structuralOnly });
    }
  }
  console.log(`plan lint mode: ${structuralOnly ? "structural" : "admission"}${writeIndex ? " + write-index" : ""}`);
  for (const warning of result.warnings) console.warn(`WARN ${warning}`);
  for (const error of result.errors) console.error(`ERROR ${error}`);
  if (result.errors.length) {
    console.error(`plan lint failed: ${result.errors.length} error(s), ${result.warnings.length} warning(s)`);
    process.exitCode = 1;
    return;
  }
  console.log(`plan lint passed: ${result.itemCount} item(s), ${result.warnings.length} warning(s)`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) runCLI();
