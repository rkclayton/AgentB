import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const chat = await readFile(new URL("./chat.js", import.meta.url), "utf8");
const css = await readFile(new URL("../css/chat.css", import.meta.url), "utf8");
const html = await readFile(new URL("../index.html", import.meta.url), "utf8");
const shell = await readFile(new URL("./shell.js", import.meta.url), "utf8");
const tokens = await readFile(new URL("../css/tokens.css", import.meta.url), "utf8");
const settings = await readFile(new URL("./settings.js", import.meta.url), "utf8");
const plan = await readFile(new URL("../plan.html", import.meta.url), "utf8");
const consoleHTML = await readFile(new URL("../index.html", import.meta.url), "utf8");

test("Chat has fence-only copy and documents composer keys", () => {
  assert.doesNotMatch(chat, /Copy message|messageCopy|assistantCopyText/);
  assert.match(html, /title="Send · Enter sends · Shift\+Enter newline"/);
});

test("Chat selection excludes chrome while preserving message content", () => {
  for (const selector of [".chat-speaker", ".thinking-line", ".tool-tick", ".chat-notice-row", ".chat-jump"]) {
    const at = css.indexOf(selector);
    assert.notEqual(at, -1, `${selector} missing`);
    assert.match(css.slice(at, at + 500), /user-select:\s*none/);
  }
  const content = css.slice(css.indexOf(".chat-content {"), css.indexOf(".chat-content {") + 200);
  assert.match(content, /user-select:\s*text/);
});

test("Whole Chat is the only attachment drop target and is invisible at rest", () => {
  assert.match(chat, /document\.body\.addEventListener\("dragenter"/);
  assert.match(chat, /document\.body\.classList\.add\("drop-target"\)/);
  assert.doesNotMatch(html, /chat-attachment-controls|<select[^>]+chat-exchange/);
  assert.match(html, />Browse…<\/button>/);
  assert.match(html, />From attachments<\/button>/);
  assert.match(css, /\.chat-page\.drop-target::after/);
});

test("A native attachment context refusal is visible beside its chip", () => {
  assert.match(chat, /if \(attachment\.outcome\)/);
  assert.match(chat, /outcome\.textContent = attachment\.outcome/);
  assert.match(css, /\.chat-attachment-warning \{ color: var\(--alarm\); \}/);
});

test("Composer clusters one paperclip above stop and send and pending files occupy no row when empty", () => {
  assert.match(html, /id="chat-attach"[^>]*>📎<\/button>/);
  assert.match(html, /class="chat-composer-row"/);
  assert.match(html, /class="chat-input-actions"[\s\S]*class="chat-attach-wrap"[\s\S]*class="chat-submit-actions"[\s\S]*id="chat-stop"[\s\S]*id="chat-send"/);
  assert.match(css, /\.chat-pending-attachments:empty\s*\{\s*display:\s*none/);
});

test("Pending approval is pinned above the composer with zero idle space", () => {
	assert.match(html, /id="chat-pending-approval" class="pending-approval" hidden[\s\S]*class="chat-composer-row"/);
	assert.match(chat, /session\?\.pending_approval \|\| session\?\.pending_repo_policy \|\| session\?\.pending_bind \? "waiting for you"/);
	assert.match(chat, /pendingApproval\.hidden = !\(session\?\.pending_approval \|\| session\?\.pending_repo_policy \|\| session\?\.pending_bind\)/);
	assert.match(css, /\.pending-approval\[hidden\]\s*\{\s*display:\s*none/);
	assert.match(tokens, /\.agent-tab-robot\.waiting\{color:var\(--alarm\)/);
});

test("Composer sends during an active run and reports projected queue count", () => {
	assert.doesNotMatch(chat, /Run in progress|queue_depth/);
	assert.match(chat, /send\.onclick = submit/);
	assert.match(chat, /const queueText = queued \? `queued \(\$\{queued\}\)/);
});

test("Degraded accounting is labeled estimated in the Chat occupancy bar", () => {
  assert.match(chat, /value\.estimated \? "estimated · " : ""/);
});

test("State strip owns queue operator pending and unreachable state without chat rows", () => {
  assert.match(html, /id="chat-status-strip"[\s\S]*id="chat-notice"[\s\S]*id="chat-retry-model"/);
  assert.doesNotMatch(html + chat, /chat-run-as-you/);
  assert.match(chat, /model unreachable · \$\{unreachable\.host/);
	assert.match(chat, /model busy · \$\{busy\.host/);
	assert.match(chat, /session\.server_id \|\| session\.b_profile/);
  assert.match(chat, /queued \(\$\{queued\}\).*waiting for model/);
  assert.match(chat, /operator mode · until/);
  assert.match(chat, /filter\(\(entry\) => !\["operator\.context", "message\.queued", "run\.queued"\]/);
  assert.match(css, /\.chat-status-strip \{ min-height:24px/);
});

test("Operator mode lives only in Settings Security and states the defeated boundary", () => {
  assert.match(settings, /Run everything as me for 20 minutes/);
  assert.match(settings, /defeats the service-account OS boundary for every tool in every chat/);
  assert.match(settings, /data-action="operator-context"/);
  assert.match(settings, /api\("\/api\/config", \{shell:\{operator_context:!store\.shell_identity\?\.operator_context\}\}\)/);
  assert.match(settings, /const serverProfiles = \(\) => Array\.isArray\(store\.servers\) \? store\.servers : \[\]/);
  assert.doesNotMatch(shell, /shell-operator-status/);
});

test("No-agent and blank Plan wells are explicit and Console links to active tools", () => {
  assert.match(chat, /No agent connected — add one in/);
  assert.match(chat, /operator-off-48\.png/);
  assert.match(plan, />No plan yet\.<\/span>/);
  assert.match(consoleHTML, /id="console-tools-link"[^>]*>0 tools active<\/a>/);
});

test("New chat uses the in-tab plus and history uses the agent right-click menu", () => {
  assert.match(html, /id="app-shell"[^>]+data-page="console"/);
  assert.match(shell, /button\("\+", `New chat with \$\{agentID\}`, "agent-tab-new"\)/);
  assert.match(shell, /oncontextmenu/);
  assert.match(shell, /agent-chat-rename/);
  assert.match(shell, /agent-chat-delete/);
  assert.doesNotMatch(html + css, /chat-list|chat-list-toggle/);
  assert.doesNotMatch(html, /chat-clear-conversation|Clear conversation/);
  assert.match(shell, /source_session_id: source\.id, workspace/);
  assert.match(shell, /Stop it before closing the chat/);
});

test("New chat binds a default, recent, or operator-picked directory", () => {
  assert.match(shell, /menu\.hidden = true/);
  assert.match(shell, /api\("\/api\/pick-folder", undefined, "GET"\)/);
  assert.match(shell, /api\("\/api\/pick-folder", \{ default: choices\.default \}\)/);
  assert.match(shell, /\{ source_session_id: source\.id, workspace \}/);
});

test("Composer is five lines with no placeholder and expands upward", () => {
  assert.match(html, /textarea id="chat-task" rows="5" aria-label="Task"><\/textarea>/);
  assert.doesNotMatch(html, /placeholder=/);
  assert.match(css, /height:\s*112px/);
	assert.match(css, /\.chat-composer\.expanded textarea[\s\S]*height:\s*min\(50vh, 520px\)/);
	assert.match(css, /grid-template-columns:\s*minmax\(0, 1fr\)/);
	assert.match(css, /\.chat-pending-attachments,[\s\S]*\.chat-input-wrap\s*\{\s*grid-column:\s*1/);
	assert.match(css, /\.chat-input-wrap \{[^}]*border-radius:8px;[^}]*box-shadow:inset/);
	assert.match(css, /\.chat-composer textarea \{[\s\S]*?padding:\s*7px 64px 7px 9px;[\s\S]*?border:\s*0;[\s\S]*?border-radius:\s*8px;/);
	assert.match(css, /\.chat-input-actions \{[^}]*right:6px;[^}]*bottom:6px;[^}]*flex-direction:column/);
	assert.match(css, /#chat-expand\s*\{[\s\S]*position:\s*absolute;[\s\S]*top:\s*4px;[\s\S]*right:\s*4px/);
  assert.match(html, /id="chat-send"[^>]+aria-label="Send"[^>]*>↵<\/button>/);
  assert.match(css, /#chat-send \{[\s\S]*?height: 24px;[\s\S]*?min-height: 24px;/);
  assert.match(css, /\.chat-input-actions \.stop-sign \{ width:24px; height:24px;/);
  assert.doesNotMatch(html, />Send<\/button>/);
});

test("Chat uses the narrow monospace label gutter and the agent_b tab restores its robot", () => {
	assert.match(css, /\.chat-entry\s*\{[\s\S]*grid-template-columns:\s*72px minmax\(0, 1fr\)/);
	assert.match(chat, /speaker\(agentAuthor\(session\), true\)/);
	assert.match(chat, /function speaker\(name, agent = false\)[\s\S]*if \(agent\)[\s\S]*assets\/agent\.svg/);
	assert.match(css, /\.chat-render-failure \.chat-content\s*\{[\s\S]*white-space:\s*nowrap/);
	assert.match(shell, /agentID === "agent_b"[\s\S]*agent-tab-robot[\s\S]*assets\/agent\.svg/);
	assert.match(tokens, /\.agent-tab-robot\{[^}]*width:20px;height:20px/);
});

test("Model-unreachable transcript notices are flat and stay outside response counts", () => {
	assert.match(chat, /filter\(hasVisibleChatContent\)/);
	assert.match(chat, /entry\.event\?\.type === "run\.stopped" && entry\.event\?\.data\?\.reason === "model_unreachable"[\s\S]*grouped\.push\(entry\);[\s\S]*response = null/);
	assert.match(chat, /entry\.text \|\| session\.model_unreachable\?\.host/);
	assert.doesNotMatch(chat, /data\.reason === "model_unreachable"[\s\S]{0,500}createElement\("details"\)/);
	assert.match(chat, /retryModel\.onclick/);
});

test("Repository policy is a pinned full-content trust decision", () => {
  assert.match(chat, /Trust this repo's policy\?/);
  assert.match(chat, /policy\.content/);
  assert.match(chat, /policy\.diff/);
  assert.match(chat, /\["Yes, for this chat","policy-approve"\]/);
  assert.match(chat, /\["Just once","policy-once"\]/);
  assert.match(chat, /\["No","policy-deny"\]/);
  assert.match(chat, /Technical detail/);
});

test("An existing outside directory raises the workspace bind offer before the model runs", () => {
	assert.match(chat, /Bind this chat to \$\{session\.pending_bind\.dir\}\?/);
	assert.match(chat, /api\("\/api\/bind", \{ session_id: session\.id, decision \}\)/);
	assert.match(chat, /pending_approval \|\| session\?\.pending_repo_policy \|\| session\?\.pending_bind \? "waiting for you"/);
});

test("Reduced motion remains zero-duration", () => {
  assert.match(css, /prefers-reduced-motion:\s*reduce[\s\S]*animation-duration:\s*0ms\s*!important/);
});
