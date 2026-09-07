import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";

const index = fs.readFileSync(new URL("../index.html", import.meta.url), "utf8");
const script = fs.readFileSync(new URL("app.js", import.meta.url), "utf8");
const styles = fs.readFileSync(new URL("../css/app.css", import.meta.url), "utf8");

test("Console launches Chat without retaining a task composer", () => {
  assert.match(index, /<a id="chat-launch"[^>]+href="\/chat"/);
  assert.doesNotMatch(index, /id="(?:composer|task)"/);
  assert.doesNotMatch(script, /getElementById\("(?:composer|task)"\)/);
  assert.match(script, /chatLaunch\.href = `\/chat\$\{suffix\}`/);
});

test("Activity uses the full panel height after composer removal", () => {
  const flowRules = [...styles.matchAll(/\.flow-well\s*\{([^}]+)\}/g)].map((match) => match[1]);
  assert.ok(flowRules.length >= 2);
  for (const rule of flowRules) assert.doesNotMatch(rule, /grid-template-rows:[^;]*1fr[^;]*\d+px/);
  assert.doesNotMatch(styles, /\.composer(?:\s|\{|\.)/);
});

test("Console renders the release tag, commit, and signature from the state snapshot", () => {
  assert.match(index, /id="build-id" class="build-id"/);
  assert.match(index, /id="signature-state" class="signature-state"/);
  assert.match(script, /renderBuildHeader\([^;]+store\.build, store\.signature\)/);
  assert.match(styles, /\.build-id\s*\{[^}]*font-family:\s*"IBM Plex Mono"/s);
});
