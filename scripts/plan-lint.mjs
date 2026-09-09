#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const scriptRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
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

const planPath = path.join(root, "PLAN.md");
const itemRoots = [
  { directory: path.join(root, "plan", "items"), label: "items" },
  { directory: path.join(root, "plan", "archive"), label: "archive" },
];
const validStates = new Set(["proposed", "live", "shipped", "superseded", "dead"]);
const validKinds = new Set(["defect", "feature", "decision", "discovery"]);
const validSurfaces = new Set(["chat", "chat-list", "composer", "tab-strip", "console", "settings", "run-loop", "accounting", "tools", "install", "plan", "tests"]);
const validMetadata = new Set(["state", "milestone", "shipped", "kind", "surfaces", "evidence", "acceptance", "depends-on", "agent"]);
const errors = [];
const warnings = [];
const items = new Map();
const sourceTexts = [];

function lineCount(text) {
  return text === "" ? 0 : text.split(/\r?\n/).length - (text.endsWith("\n") ? 1 : 0);
}

function parseMetadata(text, relative) {
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

for (const rootInfo of itemRoots) {
  if (!fs.existsSync(rootInfo.directory)) {
    errors.push(`missing directory: ${path.relative(root, rootInfo.directory)}`);
    continue;
  }
  for (const name of fs.readdirSync(rootInfo.directory).sort()) {
    if (!name.endsWith(".md")) continue;
    const id = name.slice(0, -3);
    const relative = path.posix.join("plan", rootInfo.label, name);
    const text = fs.readFileSync(path.join(rootInfo.directory, name), "utf8");
    sourceTexts.push({ relative, text });
    const parsed = parseMetadata(text, relative);
    if (!parsed) continue;
    const { metadata, body } = parsed;
    const state = metadata.get("state");
    const milestone = metadata.get("milestone");
    const firstKeys = [...metadata.keys()];
    if (firstKeys[0] !== "state" || firstKeys[1] !== "milestone") errors.push(`${relative}: state and milestone must be the first two metadata fields`);
    if (!validStates.has(state)) errors.push(`${relative}: invalid state ${JSON.stringify(state)}`);
    if (!/^(?:unknown|-|0\.\d+)$/.test(milestone ?? "")) errors.push(`${relative}: invalid milestone ${JSON.stringify(milestone)}`);
    if (rootInfo.label === "items" && !["proposed", "live"].includes(state)) errors.push(`${relative}: state ${state} must not live in plan/items`);
    if (rootInfo.label === "archive" && ["proposed", "live"].includes(state)) errors.push(`${relative}: state ${state} must not live in plan/archive`);

    const required = state === "live" ? ["state", "milestone", "kind", "surfaces", "evidence", "acceptance"]
      : state === "proposed" ? ["state", "milestone", "kind", "surfaces", "evidence"] : ["state", "milestone"];
    for (const field of required) if (!metadata.has(field) || metadata.get(field) === "") errors.push(`${relative}: missing required metadata ${field}`);
    const kind = metadata.get("kind");
    if (kind && kind !== "unknown" && !validKinds.has(kind)) errors.push(`${relative}: invalid kind ${JSON.stringify(kind)}`);
    const rawSurfaces = metadata.get("surfaces");
    const surfaces = rawSurfaces?.split(",").map((value) => value.trim()).filter(Boolean) ?? [];
    if (rawSurfaces && rawSurfaces !== "unknown" && (!surfaces.length || surfaces.some((surface) => !validSurfaces.has(surface)))) errors.push(`${relative}: invalid surfaces ${JSON.stringify(rawSurfaces)}`);
    for (const field of ["milestone", "kind", "surfaces", "evidence", "acceptance"]) {
      if (metadata.get(field) === "unknown") warnings.push(`${relative}: unresolved metadata ${field}`);
    }

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
    items.set(id, { id, state, milestone, kind: kind ?? "", surfaces, rawSurfaces: rawSurfaces ?? "", relative, title: heading[2], lines, unresolved: unresolved ? unresolved[1].trim() : null, metadata });
  }
}

for (const extra of ["plan/_reference.md", "plan/_history.md"]) {
  const full = path.join(root, ...extra.split("/"));
  if (fs.existsSync(full)) sourceTexts.push({ relative: extra, text: fs.readFileSync(full, "utf8") });
}
for (const { relative, text } of sourceTexts) {
  for (const match of text.matchAll(/\[\[([0-9]+[a-z]*)\]\]/g)) if (!items.has(match[1])) errors.push(`${relative}: unresolved item reference [[${match[1]}]]`);
}

function sortedItems() {
  return [...items.values()].sort((a, b) => a.state.localeCompare(b.state) || a.milestone.localeCompare(b.milestone, "en", { numeric: true }) || a.id.localeCompare(b.id, "en", { numeric: true }));
}

function escapeCell(value) {
  return String(value).replaceAll("|", "\\|").replaceAll("\n", " ");
}

function indexSection() {
  const rows = sortedItems().map((item) => `| ${item.state} | ${item.kind || "-"} | ${item.milestone} | [${item.id}](${item.relative}) | ${escapeCell(item.title)} | ${escapeCell(item.rawSurfaces || "-")} | ${item.lines} |`);
  return [
    "## Index", "", "Generated from item files. This is navigation, not an execution priority queue.", "",
    "| State | Kind | Milestone | Item | Title | Surfaces | Lines |",
    "| --- | --- | --- | --- | --- | --- | ---: |", ...rows, "",
  ].join("\n");
}

if (!fs.existsSync(planPath)) {
  errors.push("missing PLAN.md");
} else {
  let plan = fs.readFileSync(planPath, "utf8");
  if (writeIndex && !errors.length) {
    if (!/^## Index\s*$/m.test(plan)) errors.push("PLAN.md: missing ## Index");
    else {
      const newline = plan.includes("\r\n") ? "\r\n" : "\n";
      plan = plan.replace(/^## Index\s*$[\s\S]*$/m, indexSection()).replaceAll("\n", newline);
      fs.writeFileSync(planPath, plan, "utf8");
    }
  }

  for (const match of plan.matchAll(/\[\[([0-9]+[a-z]*)\]\]/g)) if (!items.has(match[1])) errors.push(`PLAN.md: unresolved item reference [[${match[1]}]]`);
  const indexMatch = plan.match(/^## Index\s*$[\s\S]*$/m);
  if (!indexMatch) errors.push("PLAN.md: missing ## Index");
  else if (indexMatch[0].replaceAll("\r\n", "\n").replace(/\s+$/, "") !== indexSection().replace(/\s+$/, "")) errors.push("PLAN.md index: stale or malformed; run node scripts/plan-lint.mjs --write-index --structural");

  if (/^## (?:Completed|Closed|Previous) work order\b/im.test(plan)) errors.push("PLAN.md: completed-order heading is not allowed");
  const currentMatch = plan.match(/^## Current work order([^\n]*)\n([\s\S]*?)(?=^## (?:Next work order|In flight|Index)|(?![\s\S]))/m);
  if (!currentMatch) errors.push("PLAN.md: cannot find Current work order");
  else {
    const currentText = currentMatch[2];
    const orderId = currentText.match(/^Order ID:\s*`([^`]+)`/m)?.[1] ?? currentMatch[1].match(/\b(v\d+\.\d+\.\d+|[A-Z][A-Z0-9-]+)\b/)?.[1];
    const inFlight = plan.match(/^## In flight\s*$\n([\s\S]*?)(?=^## |(?![\s\S]))/m)?.[1] ?? "";
    if (orderId) {
      for (const marker of inFlight.matchAll(/^-\s+`?([^\s`/]+)\/(W\d+)/gm)) if (marker[1] !== orderId) errors.push(`PLAN.md: In flight marker ${marker[1]}/${marker[2]} belongs to another order (current ${orderId})`);
    }

    if (!structuralOnly && !/No product changes/i.test(currentText)) {
      const workItems = [...currentText.matchAll(/^- W\d+\s+\*\*(?:item\s+)?([0-9]+[a-z]*)\b([^\n]*)/gmi)];
      const executable = new Map();
      for (const match of workItems) {
        const clauseStart = match.index;
        const after = currentText.slice(clauseStart + 1);
        const next = after.search(/\n- W\d+\s+/);
        const id = match[1].toLowerCase();
        const clause = next < 0 ? currentText.slice(clauseStart) : currentText.slice(clauseStart, clauseStart + 1 + next);
        executable.set(id, `${executable.get(id) ?? ""}\n${clause}`);
      }
      if (!executable.size) errors.push("ORDER GATE: no executable item IDs found in W headings");
      for (const [id, clause] of executable) {
        const item = items.get(id);
        if (!item) {
          errors.push(`ORDER GATE: executable item ${id} does not exist`);
          continue;
        }
        if (item.state !== "live") errors.push(`ORDER GATE: executable item ${id} is ${item.state}, expected live`);
        for (const field of ["milestone", "kind", "surfaces", "evidence", "acceptance"]) {
          const value = item.metadata.get(field);
          if (!value || value === "unknown" || (field === "milestone" && value === "-")) errors.push(`ORDER GATE: executable item ${id} has unresolved ${field}`);
        }
        const authorizationRecord = `${clause}\n${item.metadata.get("evidence") ?? ""}`;
        if (!/\boperator\b|\bauthori[sz](?:e|ed|ation)\b/i.test(authorizationRecord)) errors.push(`ORDER GATE: executable item ${id} has no recorded authorization for this scope`);
        if (item.unresolved !== "(none)") {
          if (/DISCOVERY BEFORE FIX|DISCOVERY FIRST|\bEstablish\b[\s\S]*?before/i.test(clause)) warnings.push(`ORDER GATE: item ${id} unresolved entry is explicitly covered by discovery-first work`);
          else errors.push(`ORDER GATE: executable item ${id} has non-empty ## Unresolved not covered by its W clause`);
        }
      }
    }
  }
}

console.log(`plan lint mode: ${structuralOnly ? "structural" : "admission"}${writeIndex ? " + write-index" : ""}`);
for (const warning of warnings) console.warn(`WARN ${warning}`);
for (const error of errors) console.error(`ERROR ${error}`);
if (errors.length) {
  console.error(`plan lint failed: ${errors.length} error(s), ${warnings.length} warning(s)`);
  process.exit(1);
}
console.log(`plan lint passed: ${items.size} item(s), ${warnings.length} warning(s)`);
