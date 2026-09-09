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
const pages = await Promise.all(["index.html", "chat.html", "plan.html"].map(async (name) => [name, await readFile(new URL(`../${name}`, import.meta.url), "utf8")]));

test("shared shell slot order is identical on Chat Console and Plan", () => {
  for (const [name, html] of pages) {
    assert.match(html, new RegExp(`id="app-shell"[^>]+data-page="${name === "index.html" ? "console" : name.slice(0, -5)}"`));
    assert.doesNotMatch(html, /id="(?:shell-stop|shell-state|shell-operator-status)"/);
  }
  assert.match(shell, /root\.append\(left, right\)/);
  assert.match(shell, /right\.append\(pages, settings\)/);
  assert.doesNotMatch(shell, /shell-operator-status|right\.append\(stop/);
  assert.match(shell, /\[\["plan", "Plan", "\/plan"\]\]/);
  assert.doesNotMatch(shell, /\["chat", "Chat", "\/chat"\]|\["console", "Console", "\/"\]/);
});

test("agent tabs are ordered and expose idle running waiting glyphs", () => {
  assert.match(shell, /const agents = \["agent_b"\]/);
  assert.match(shell, /configured\?\.c\) agents\.push\("agent_c"\)/);
  assert.match(shell, /configured\?\.d\) agents\.push\("agent_d"\)/);
  assert.doesNotMatch(shell, /agents\.push\("agent_a"\)/);
  assert.match(shell, /return "waiting"[\s\S]*return "running"[\s\S]*return "idle"/);
  assert.match(shell, /glyphState === "waiting" \? "!" : glyphState === "running" \? "●" : "○"/);
  assert.match(tokens, /\.agent-state\.running\{color:var\(--trace\)\}/);
  assert.match(tokens, /\.agent-state\.offline\{color:var\(--alarm\)\}/);
  assert.match(tokens, /\.agent-state\.waiting,.shell-state\.waiting\{color:var\(--alarm\)\}/);
  assert.match(shell, /sessions\.some\(\(item\) => item\.model_unreachable\)\) return "offline"/);
  assert.match(shell, /\(store\.servers \|\| \[\]\)\.find/);
  assert.match(shell, /button\("\+", `New chat with \$\{agentID\}`, "agent-tab-new"\)/);
  assert.match(tokens, /\.agent-tab-wrap\{[^}]*flex:0 1 auto/);
  assert.match(shell, /location\.assign\(next === "chat" \? `\/chat\$\{suffix\}` : `\/\$\{suffix\}`\)/);
  assert.match(shell, /agentb\.side\.\$\{agentID\}/);
  assert.match(tokens, /\.agent-tab\.side-chat\{color:var\(--ink\)\}/);
  assert.match(tokens, /\.agent-tab\.side-console\{color:var\(--trace\)\}/);
});

test("agent menu is the counted open and closed chat history with glyph controls", () => {
  assert.match(shell, /sessionsFor\(agentID, true\)/);
  assert.match(shell, /oncontextmenu/);
  assert.match(shell, /button\("Open"/);
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

test("overflow menu keeps extra Agents reachable without a horizontal scrollbar", () => {
  assert.match(shell, /agentTabLayout\(entries\.length, tabs\.clientWidth\)/);
  assert.match(shell, /button\("", "More agents", "agent-overflow"\)/);
  assert.match(shell, /overflowButton\.textContent = `\+\$\{hidden\.length\}`/);
  assert.doesNotMatch(tokens + appCSS + chatCSS, /overflow(?:-x)?:\s*(?:auto|scroll)/);
});

test("Stop follows the selected chat from each page-local lower control", () => {
  assert.match(chat, /api\("\/api\/stop", \{ session_id: session\.id \}\)/);
  assert.match(consoleApp, /api\("\/api\/stop",\{session_id:id\}\)/);
  assert.match(plan, /api\("\/api\/stop", \{session_id:id\}\)/);
  assert.doesNotMatch(chat+consoleApp+plan, /all:\s*true/);
});

test("top bar belongs to shrinking non-scrolling agent tabs and right controls", () => {
  assert.match(tokens, /\.shell-left,\.agent-tabs\{overflow:hidden/);
  assert.match(tokens, /\.agent-tab-wrap\{[^}]*min-width:72px/);
  assert.match(tokens, /\.agent-tab[^\n]*white-space:nowrap/);
  assert.doesNotMatch(shell, /shell-selection/);
});

test("all shell motion is zero duration under reduced motion", () => {
  assert.match(tokens, /prefers-reduced-motion:reduce[\s\S]*\.app-shell[\s\S]*animation-duration:0ms!important/);
});
