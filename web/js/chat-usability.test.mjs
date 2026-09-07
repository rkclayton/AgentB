import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const chat = await readFile(new URL("./chat.js", import.meta.url), "utf8");
const css = await readFile(new URL("../css/chat.css", import.meta.url), "utf8");
const html = await readFile(new URL("../chat.html", import.meta.url), "utf8");
const shell = await readFile(new URL("./shell.js", import.meta.url), "utf8");
const tokens = await readFile(new URL("../css/tokens.css", import.meta.url), "utf8");

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
  assert.match(html, />From exchange folder<\/button>/);
  assert.match(css, /\.chat-page\.drop-target::after/);
});

test("Composer uses one paperclip and pending files occupy no row when empty", () => {
  assert.match(html, /id="chat-attach"[^>]*>📎<\/button>/);
  assert.match(html, /class="chat-composer-row"/);
  assert.match(css, /\.chat-pending-attachments:empty\s*\{\s*display:\s*none/);
});

test("Pending approval is pinned above the composer with zero idle space", () => {
	assert.match(html, /id="chat-pending-approval" class="pending-approval" hidden[\s\S]*class="chat-composer-row"/);
	assert.match(chat, /session\?\.pending_approval \|\| session\?\.pending_repo_policy \? "waiting for you"/);
	assert.match(chat, /pendingApproval\.hidden = !\(session\?\.pending_approval \|\| session\?\.pending_repo_policy\)/);
	assert.match(css, /\.pending-approval\[hidden\]\s*\{\s*display:\s*none/);
	assert.match(tokens, /\.shell-state\.waiting\{color:var\(--alarm\)/);
});

test("Composer sends during an active run and reports projected queue count", () => {
	assert.doesNotMatch(chat, /Run in progress|queue_depth/);
	assert.match(chat, /send\.onclick = submit/);
	assert.match(chat, /queued \? `queued \(\$\{queued\}\)`/);
});

test("New, list, close, and rename live in the shared agent-tab shell", () => {
  assert.match(html, /id="app-shell"[^>]+data-page="chat"/);
  assert.match(shell, /className = "shell-add"|"shell-add"/);
  assert.match(shell, /oncontextmenu/);
  assert.match(shell, /agent-chat-rename/);
  assert.doesNotMatch(html, /chat-clear-conversation|Clear conversation/);
  assert.match(shell, /source_session_id: source\.id, workspace/);
  assert.match(shell, /Stop it before closing the chat/);
});

test("New chat binds a default, recent, or operator-picked directory", () => {
  assert.match(shell, /addMenu\.hidden = true/);
  assert.match(shell, /api\("\/api\/pick-folder", undefined, "GET"\)/);
  assert.match(shell, /api\("\/api\/pick-folder", \{ default: choices\.default \}\)/);
  assert.match(shell, /session\.workspace_dir \|\| session\.workspace/);
});

test("Composer is five lines with no placeholder and expands upward", () => {
  assert.match(html, /textarea id="chat-task" rows="5" aria-label="Task"><\/textarea>/);
  assert.doesNotMatch(html, /placeholder=/);
  assert.match(css, /height:\s*112px/);
  assert.match(css, /\.chat-composer\.expanded textarea[\s\S]*height:\s*min\(50vh, 520px\)/);
  assert.match(css, /grid-template-columns:\s*24px auto minmax\(0, 1fr\) 24px 72px/);
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

test("Reduced motion remains zero-duration", () => {
  assert.match(css, /prefers-reduced-motion:\s*reduce[\s\S]*animation-duration:\s*0ms\s*!important/);
});
