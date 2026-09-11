import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";

const index = fs.readFileSync(new URL("../index.html", import.meta.url), "utf8");
const script = fs.readFileSync(new URL("app.js", import.meta.url), "utf8");
const styles = fs.readFileSync(new URL("../css/app.css", import.meta.url), "utf8");
const shell = fs.readFileSync(new URL("shell.js", import.meta.url), "utf8");
const settings = fs.readFileSync(new URL("settings.js", import.meta.url), "utf8");

test("Console uses the shared shell without retaining a task composer", () => {
  assert.match(index, /id="app-shell"[^>]+data-page="console"/);
  assert.doesNotMatch(index, /id="(?:composer|task)"/);
  assert.doesNotMatch(script, /getElementById\("(?:composer|task)"\)/);
  assert.doesNotMatch(shell, /\["chat", "Chat", "\/chat"\]|\["console", "Console", "\/"\]/);
  assert.match(shell, /\[\["plan", "\/plan"\]\]/);
  assert.match(shell, /requestNavigation\(navigation, next === "chat"/);
});

test("Activity uses the full panel height after composer removal", () => {
  const flowRules = [...styles.matchAll(/\.flow-well\s*\{([^}]+)\}/g)].map((match) => match[1]);
  assert.ok(flowRules.length >= 2);
  for (const rule of flowRules) assert.doesNotMatch(rule, /grid-template-rows:[^;]*1fr[^;]*\d+px/);
  assert.doesNotMatch(styles, /\.composer(?:\s|\{|\.)/);
});

test("Console header omits build identity and Settings owns About", () => {
  assert.doesNotMatch(index, /build-id|signature-state/);
  assert.doesNotMatch(script, /renderBuildHeader/);
  assert.match(settings, /\["about", "About"\]/);
  assert.match(settings, /function about\(\)/);
});

test("Settings navigation remains install-global while agent controls live on Console", () => {
  assert.doesNotMatch(settings, /\["sessions", "Sessions"\]|\["tools", "Tools"\]|\["memory", "Memory"\]|\["session", "Current session"\]/);
  assert.match(index, /id="console-agent"/);
  assert.match(index, /id="console-agent-server"/);
  assert.match(index, /id="console-agent-server-state" role="status"/);
  assert.match(index, /id="console-agent-server-cancel"[^>]+hidden/);
  assert.match(script, /Applied \$\{agent\.b\} · pending \$\{pending\.to\}/);
  assert.match(index, /id="console-tools"/);
  assert.match(index, /id="flush-memory"/);
});

test("Console pins the current approval and shows waiting for you in state colour", () => {
	assert.match(index, /id="console-pending-approval" class="pending-approval" hidden/);
	assert.match(script, /renderPendingApproval\(session\)/);
	assert.match(styles, /\.pending-approval\[hidden\]\s*\{\s*display:\s*none/);
	const flow = fs.readFileSync(new URL("flow.js", import.meta.url), "utf8");
	assert.match(flow, /session\.pending_approval \? "waiting for you"/);
	assert.match(flow, /classList\.toggle\("alarm"/);
});

test("Drop last message is relocated beside the latest History turn and Clear is absent", () => {
  assert.match(index, /id="drop-last-message"/);
  assert.match(script, /target\.append\(dropLastMessage\)/);
  assert.doesNotMatch(index + script, /clear-conversation|Clear conversation|createSessionResetController/);
});
