#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const planPath = path.join(root, "PLAN.md");
const itemRoots = [
  { directory: path.join(root, "plan", "items"), label: "items" },
  { directory: path.join(root, "plan", "archive"), label: "archive" },
];
const args = new Set(process.argv.slice(2));
const knownArgs = new Set(["--structural"]);
const unknownArgs = [...args].filter((arg) => !knownArgs.has(arg));
if (unknownArgs.length) {
  console.error(`unknown argument(s): ${unknownArgs.join(", ")}`);
  process.exit(2);
}
const structuralOnly = args.has("--structural");
const validStates = new Set(["proposed", "live", "shipped", "superseded", "dead"]);
const validKinds = new Set(["defect", "feature", "decision", "discovery"]);
const validSurfaces = new Set(["chat-list", "composer", "tab-strip", "console", "settings", "run-loop", "accounting", "tools", "install", "plan", "tests"]);
const validMetadata = new Set(["state", "milestone", "shipped", "kind", "surfaces", "evidence", "acceptance"]);
const errors = [];
const warnings = [];
const items = new Map();

function lineCount(text) {
  return (text.match(/\n/g) || []).length + (text.endsWith("\n") ? 0 : 1);
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
    const frontmatter = text.match(/^((?:[a-z][a-z_]*: [^\n]*\n)+)\n/);
    if (!frontmatter) {
      errors.push(`${relative}: invalid frontmatter shape`);
      continue;
    }
    const metadata = new Map();
    for (const line of frontmatter[1].trimEnd().split("\n")) {
      const separator = line.indexOf(": ");
      const key = line.slice(0, separator), value = line.slice(separator + 2);
      if (!validMetadata.has(key)) errors.push(`${relative}: unknown metadata field ${JSON.stringify(key)}`);
      if (metadata.has(key)) errors.push(`${relative}: duplicate metadata field ${JSON.stringify(key)}`);
      metadata.set(key, value);
    }
    const state = metadata.get("state"), milestone = metadata.get("milestone");
    const firstLines = frontmatter[1].split("\n");
    if (!firstLines[0]?.startsWith("state: ") || !firstLines[1]?.startsWith("milestone: ")) {
      errors.push(`${relative}: state and milestone must be the first two metadata fields`);
    }
    if (!validStates.has(state)) {
      errors.push(`${relative}: invalid state ${JSON.stringify(state)}`);
    }
    if (!/^(?:-|0\.\d+)$/.test(milestone)) {
      errors.push(`${relative}: invalid milestone ${JSON.stringify(milestone)}`);
    }
    if (rootInfo.label === "items" && !["proposed", "live"].includes(state)) {
      errors.push(`${relative}: state ${state} must not live in plan/items`);
    }
    if (rootInfo.label === "archive" && ["proposed", "live"].includes(state)) {
      errors.push(`${relative}: state ${state} must not live in plan/archive`);
    }
    if (metadata.has("kind") && !validKinds.has(metadata.get("kind"))) {
      errors.push(`${relative}: invalid kind ${JSON.stringify(metadata.get("kind"))}`);
    }
    if (metadata.has("surfaces")) {
      const surfaces = metadata.get("surfaces").split(",").map((value) => value.trim()).filter(Boolean);
      if (!surfaces.length || surfaces.some((surface) => !validSurfaces.has(surface))) {
        errors.push(`${relative}: invalid surfaces ${JSON.stringify(metadata.get("surfaces"))}`);
      }
    }

    const heading = text.slice(frontmatter[0].length).match(/^# ([0-9]+[a-z]*) — ([^\n]+)$/m);
    if (!heading) {
      errors.push(`${relative}: missing top-level item heading`);
      continue;
    }
    if (heading[1] !== id) {
      errors.push(`${relative}: heading id ${heading[1]} does not match filename ${id}`);
    }
    if (items.has(id)) {
      errors.push(`${relative}: duplicate item id ${id}`);
      continue;
    }

    const unresolved = text.match(/\n## Unresolved\n\n([\s\S]*)$/);
    if (!unresolved) {
      errors.push(`${relative}: missing ## Unresolved`);
    }
    if (state === "shipped" && !/\bv\d+\.\d+\.\d+\b|\b[0-9a-f]{7,40}\b/i.test(text)) {
      warnings.push(`${relative}: shipped item names no tag or commit`);
    }
    const lines = lineCount(text);
    if (lines > 100) {
      warnings.push(`${relative}: long item (${lines} lines)`);
    }
    items.set(id, {
      id,
      state,
      milestone,
      relative,
      title: heading[2],
      lines,
      unresolved: unresolved ? unresolved[1].trim() : null,
    });
  }
}

if (!fs.existsSync(planPath)) {
  errors.push("missing PLAN.md");
} else {
  const plan = fs.readFileSync(planPath, "utf8");
  const indexStart = plan.indexOf("## Index\n");
  if (indexStart < 0) {
    errors.push("PLAN.md: missing ## Index");
  } else {
    const indexed = new Map();
    const rowPattern = /^\| (proposed|live|shipped|superseded|dead) \| ([^|]+) \| \[([0-9]+[a-z]*)\]\(([^)]+)\) \| (.*) \| (\d+) \|$/gm;
    const indexText = plan.slice(indexStart);
    const tableHeader = "| State | Milestone | Item | Title | Lines |";
    const tableStart = indexText.indexOf(tableHeader);
    const tableText = tableStart < 0 ? "" : indexText.slice(tableStart);
    if (tableStart < 0) errors.push("PLAN.md index: missing table header");
    else {
      const tableLines = tableText.split("\n");
      if (tableLines[1] !== "| --- | --- | --- | --- | ---: |") errors.push("PLAN.md index: malformed table separator");
      for (const line of tableLines.slice(2)) {
        if (line && !rowPattern.test(line)) errors.push(`PLAN.md index: malformed row ${JSON.stringify(line)}`);
        rowPattern.lastIndex = 0;
      }
    }
    for (const match of tableText.matchAll(rowPattern)) {
      const [, state, rawMilestone, id, relative, title, rawLines] = match;
      if (indexed.has(id)) errors.push(`PLAN.md index: duplicate item ${id}`);
      indexed.set(id, {
        state,
        milestone: rawMilestone.trim(),
        relative,
        title: title.replaceAll("\\|", "|"),
        lines: Number(rawLines),
      });
    }
    for (const [id, item] of items) {
      const row = indexed.get(id);
      if (!row) {
        errors.push(`PLAN.md index: missing item ${id}`);
        continue;
      }
      for (const field of ["state", "milestone", "relative", "title", "lines"]) {
        if (row[field] !== item[field]) {
          errors.push(`PLAN.md index: item ${id} ${field} is ${JSON.stringify(row[field])}, expected ${JSON.stringify(item[field])}`);
        }
      }
    }
    for (const id of indexed.keys()) {
      if (!items.has(id)) errors.push(`PLAN.md index: unknown item ${id}`);
    }
  }

  const currentMatch = plan.match(
    /^## Current work order[^\n]*\n([\s\S]*?)(?=^## (?:Next work order|In flight))/m,
  );
  if (!currentMatch) {
    errors.push("PLAN.md: cannot find Current/Next work-order boundary");
  } else {
    const positiveScope = currentMatch[1].replace(
      /(?:^|\n)DO NOT(?:\s*\([^\n)]*\))?:[\s\S]*?(?=\n(?:RELEASE|REPORT):|$)/,
      "\n",
    );
    const referenced = new Set();
    for (const match of positiveScope.matchAll(/\bitem\s+([0-9]+[a-z]*)\b/gi)) {
      referenced.add(match[1]);
    }
    for (const match of positiveScope.matchAll(/\[\[([0-9]+[a-z]*)\]\]/g)) {
      referenced.add(match[1]);
    }
    for (const id of referenced) {
      const item = items.get(id);
      if (!item) {
        errors.push(`ORDER GATE: referenced item ${id} does not exist`);
      } else if (!structuralOnly) {
        if (item.state !== "live") {
          errors.push(`ORDER GATE: referenced item ${id} is ${item.state}, expected live`);
        }
        if (item.unresolved !== "(none)") {
          errors.push(`ORDER GATE: referenced item ${id} has non-empty ## Unresolved`);
        }
      }
    }
  }
}

console.log(`plan lint mode: ${structuralOnly ? "structural" : "admission"}`);
for (const warning of warnings) console.warn(`WARN ${warning}`);
for (const error of errors) console.error(`ERROR ${error}`);
if (errors.length) {
  console.error(`plan lint failed: ${errors.length} error(s), ${warnings.length} warning(s)`);
  process.exit(1);
}
console.log(`plan lint passed: ${items.size} item(s), ${warnings.length} warning(s)`);
