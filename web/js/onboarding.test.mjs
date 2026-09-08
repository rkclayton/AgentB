import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";

import { recommendationForBytes, recommendationTable, recommendationTableVersion } from "./local-recommendations.js";

const html = fs.readFileSync(new URL("../setup.html", import.meta.url), "utf8");
const script = fs.readFileSync(new URL("setup.js", import.meta.url), "utf8");
const styles = fs.readFileSync(new URL("../css/setup.css", import.meta.url), "utf8");
const detector = fs.readFileSync(new URL("../../scripts/detect-local-capabilities.ps1", import.meta.url), "utf8");
const template = JSON.parse(fs.readFileSync(new URL("../../harness.example.json", import.meta.url), "utf8"));

test("Fresh template has no servers and setup offers the three skippable paths", () => {
  assert.deepEqual(template.servers, []);
  assert.deepEqual(template.agents, []);
  for (const label of ["API only", "API + local assistant", "Full local"]) assert.match(script, new RegExp(label.replaceAll("+", "\\+")));
  assert.match(script, /Skip for now/);
  assert.match(script, /Skip this step/);
  assert.doesNotMatch(`${html}\n${script}`, /\b(?:PKI|accounting)\b/i);
});

test("Connection Test shows the six requested capabilities in plain words", () => {
  for (const label of ["Token counting", "Streaming replies", "Tool use", "PDF and document input", "Image input", "Context size"]) assert.match(script, new RegExp(label));
  assert.match(script, /\/api\/servers\/\$\{encodeURIComponent\(id\)\}\/probe/);
});

test("Hybrid sequencing preserves the API profile when Agent C is added", () => {
  assert.match(script, /snapshot\.config\.servers \|\| \[\]\)\.filter/);
  assert.match(script, /choice === "hybrid" \? \{ c: id \}/);
  assert.match(script, /current\.b \|\| id/);
});

test("Versioned recommendation table stays small and memory-class keyed", () => {
  assert.equal(recommendationTableVersion, "2026-09-07");
  assert.ok(recommendationTable.length <= 20);
  assert.equal(recommendationForBytes(8 * 1024 ** 3).model, "qwen3.5:4b");
  assert.equal(recommendationForBytes(32 * 1024 ** 3).model, "qwen3.5:27b");
  assert.equal(recommendationForBytes(128 * 1024 ** 3).model, "qwen3.5:122b");
});

test("Local detection is read-only and covers servers plus interpreters", () => {
  for (const name of ["Ollama", "LM Studio", "llama-server", "python", "node", "go", "dotnet"]) assert.match(detector, new RegExp(`name='${name}'`));
  assert.doesNotMatch(detector, /\$env:Path\s*=|Start-Process|Invoke-WebRequest|Invoke-RestMethod/);
  assert.match(detector, /runs as service/);
  assert.match(detector, /operator-only/);
});

test("Setup follows the console palette and reduced motion is zero seconds", () => {
  assert.doesNotMatch(styles, /#[0-9a-f]{3,8}/i);
  assert.match(styles, /prefers-reduced-motion:reduce/);
  assert.match(styles, /transition-duration:0ms!important;animation-duration:0ms!important/);
});
