import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const shell = await readFile(new URL("./shell.js", import.meta.url), "utf8");
const tokens = await readFile(new URL("../css/tokens.css", import.meta.url), "utf8");
const pages = await Promise.all(["index.html", "chat.html", "plan.html"].map(async (name) => [name, await readFile(new URL(`../${name}`, import.meta.url), "utf8")]));

test("shared shell slot order is identical on Chat Console and Plan", () => {
  for (const [name, html] of pages) {
    assert.match(html, new RegExp(`id="app-shell"[^>]+data-page="${name === "index.html" ? "console" : name.slice(0, -5)}"`));
    assert.doesNotMatch(html, /id="(?:shell-stop|shell-state|shell-operator-status)"/);
  }
  assert.match(shell, /root\.append\(left, middle, right\)/);
  assert.match(shell, /right\.append\(stop, state, operator, alarm, connection, pages, settings\)/);
  assert.match(shell, /\[\["chat", "Chat", "\/chat"\], \["console", "Console", "\/"\], \["plan", "Plan", "\/plan"\]\]/);
});

test("agent tabs are ordered and expose idle running waiting glyphs", () => {
  assert.match(shell, /const agents = \["agent_b"\]/);
  assert.match(shell, /configured\?\.c\) agents\.push\("agent_c"\)/);
  assert.match(shell, /configured\?\.d\) agents\.push\("agent_d"\)/);
  assert.doesNotMatch(shell, /agents\.push\("agent_a"\)/);
  assert.match(shell, /return "waiting"[\s\S]*return "running"[\s\S]*return "idle"/);
  assert.match(shell, /glyphState === "waiting" \? "!" : glyphState === "running" \? "●" : "○"/);
  assert.match(tokens, /\.agent-state\.running\{color:var\(--trace\)\}/);
  assert.match(tokens, /\.agent-state\.waiting,.shell-state\.waiting\{color:var\(--alarm\)\}/);
});

test("agent menu owns open close and inline rename for open and closed chats", () => {
  assert.match(shell, /sessionsFor\(agentID, true\)/);
  assert.match(shell, /oncontextmenu/);
  assert.match(shell, /button\("Open"/);
  assert.match(shell, /button\("Close"/);
  assert.match(shell, /button\("Rename"/);
  assert.match(shell, /agent-chat-rename-form/);
  assert.match(shell, /\{ label \}/);
});

test("Stop targets only the selected chat", () => {
  assert.match(shell, /const sessionID = store\.selection\.session_id/);
  assert.match(shell, /api\("\/api\/stop", \{ session_id: sessionID \}\)/);
  assert.doesNotMatch(shell, /all:\s*true/);
});

test("all shell motion is zero duration under reduced motion", () => {
  assert.match(tokens, /prefers-reduced-motion:reduce[\s\S]*\.app-shell[\s\S]*animation-duration:0ms!important/);
});
