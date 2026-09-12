import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const shell = await readFile(new URL("./shell.js", import.meta.url), "utf8");
const chat = await readFile(new URL("./chat.js", import.meta.url), "utf8");
const consoleApp = await readFile(new URL("./app.js", import.meta.url), "utf8");
const plan = await readFile(new URL("./plan.js", import.meta.url), "utf8");
const tokens = await readFile(new URL("../css/tokens.css", import.meta.url), "utf8");
const appCSS = await readFile(new URL("../css/app.css", import.meta.url), "utf8");
const chatCSS = await readFile(new URL("../css/chat.css", import.meta.url), "utf8");
const pages = await Promise.all(["index.html", "plan.html"].map(async (name) => [name, await readFile(new URL(`../${name}`, import.meta.url), "utf8")]));

test("shared shell slot order is identical on Chat Console and Plan", () => {
  for (const [name, html] of pages) {
    assert.match(html, new RegExp(`id="app-shell"[^>]+data-page="${name === "index.html" ? "console" : "plan"}"`));
    assert.doesNotMatch(html, /id="(?:shell-stop|shell-state|shell-operator-status)"/);
  }
  assert.match(shell, /root\.append\(left, right\)/);
  assert.match(shell, /right\.append\(pages, settings\)/);
  assert.doesNotMatch(shell, /shell-operator-status|right\.append\(stop/);
  assert.match(shell, /\[\["plan", "\/plan"\]\]/);
  assert.doesNotMatch(shell, /\["chat", "Chat", "\/chat"\]|\["console", "Console", "\/"\]/);
});

test("each open chat gets an agent tab whose robot eyes expose that chat state", () => {
  assert.match(shell, /const open = sessionsFor\(agentID, false\)/);
  assert.match(shell, /for \(const session of rendered\)/);
  assert.match(shell, /wrap\.dataset\.session = session\.id/);
  assert.match(shell, /return "waiting"[\s\S]*return "running"[\s\S]*return "idle"/);
  assert.doesNotMatch(shell, /class="agent-state/);
  assert.match(shell, /agent-tab-robot-\$\{robot\} \$\{glyphState\}/);
  assert.match(shell, /<span class="agent-tab-eyes"><\/span>/);
  assert.match(tokens, /\.agent-tab-robot\.running\{color:var\(--trace\)\}/);
  assert.match(tokens, /\.agent-tab-robot\.offline\{color:var\(--alarm\)\}/);
  assert.match(tokens, /\.agent-tab-robot\.waiting\{color:var\(--alarm\)\}/);
  assert.match(shell, /session\?\.model_unreachable\) return "offline"/);
  assert.match(shell, /\(store\.servers \|\| \[\]\)\.find/);
  assert.match(shell, /button\("\+", `New chat with \$\{agentID\}`, "agent-tab-new"\)/);
  assert.match(tokens, /--agent-tab-width:118px/);
  assert.match(tokens, /\.agent-tab-wrap\{[^}]*flex:0 0 var\(--agent-tab-width\)/);
  assert.match(tokens, /\.agent-tab\{[^}]*flex:1 1 auto;[^}]*min-width:69px/);
  assert.match(shell, /if \(options\.switchView\) options\.switchView\(next, navigation\)/);
  assert.match(shell, /agentb\.side\.\$\{agentID\}/);
  assert.match(tokens, /\.agent-tab\.side-chat\{color:var\(--ink\)\}/);
  assert.match(tokens, /\.agent-tab\.side-console\{color:var\(--ink\)\}/);
  assert.match(tokens, /\.agent-tab-wrap\.selected\.side-console\{background:rgba\(216,221,227,.16\)\}/);
  assert.match(shell, /button\("", chatName/);
  assert.match(shell, /<span>\$\{escapeHTML\(agentID\)\}<\/span>/);
  assert.match(shell, /button\("×", `Close \$\{chatName\}`/);
});

test("Plan is a compact accessible brain icon", () => {
  assert.match(shell, /class="shell-page-icon"/);
  assert.match(shell, /setAttribute\("aria-label", "plan"\)/);
  assert.match(shell, /link\.title = "plan"/);
  assert.doesNotMatch(shell, /\[\["plan", "Plan", "\/plan"\]\]/);
});

test("agent menu is the counted open and closed chat history with glyph controls", () => {
  assert.match(shell, /sessionsFor\(agentID, true\)/);
  assert.match(shell, /oncontextmenu/);
  assert.match(shell, /button\("Open"/);
  assert.match(shell, /\/reopen`/);
  assert.match(shell, /button\("×"/);
  assert.match(shell, /button\("🗑"/);
  assert.match(shell, /button\("Rename"/);
  assert.match(shell, /agent-chat-rename-form/);
  assert.match(shell, /\{ label \}/);
  assert.match(shell, /openCount[\s\S]*closed/);
  assert.match(shell, /confirm: false/);
  assert.match(shell, /delete-confirm/);
  assert.match(shell, /confirm: true, drop_memory: dropMemory\.checked/);
  assert.doesNotMatch(shell, /window\.confirm\([^)]*Delete/);
  assert.match(shell, /revealMenu\(menu, tab\)/);
  assert.match(tokens, /\.shell-menu\{position:fixed/);
  assert.match(tokens, /max-height:calc\(100vh - 50px\);overflow-x:hidden;overflow-y:auto/);
});

test("unbounded chat tabs scroll only inside the tab strip", () => {
  assert.match(tokens, /\.agent-tabs\{[^}]*overflow-x:auto;overflow-y:hidden/);
  assert.doesNotMatch(shell, /agentTabLayout|agent-overflow/);
  assert.doesNotMatch(appCSS + chatCSS, /overflow-x:\s*(?:auto|scroll)/);
});

test("Stop follows the selected chat from each page-local lower control", () => {
  assert.match(chat, /api\("\/api\/stop", \{ session_id: session\.id \}\)/);
  assert.match(consoleApp, /api\("\/api\/stop",\{session_id:id\}\)/);
  assert.match(plan, /api\("\/api\/stop", \{session_id:id\}\)/);
  assert.doesNotMatch(chat+consoleApp+plan, /all:\s*true/);
});

test("top bar gives fixed readable tabs a scoped horizontal scroll lane", () => {
  assert.match(tokens, /\.shell-left\{overflow:hidden/);
  assert.match(tokens, /\.agent-tabs\{[^}]*overflow-x:auto/);
  assert.match(tokens, /\.agent-tab-wrap\{[^}]*min-width:var\(--agent-tab-width\)/);
  assert.match(tokens, /\.agent-tab[^\n]*white-space:nowrap/);
  assert.doesNotMatch(shell, /shell-selection/);
});

test("all shell motion is zero duration under reduced motion", () => {
  assert.match(tokens, /prefers-reduced-motion:reduce[\s\S]*\.app-shell[\s\S]*animation-duration:0ms!important/);
});
