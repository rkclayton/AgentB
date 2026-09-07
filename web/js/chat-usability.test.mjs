import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const chat = await readFile(new URL("./chat.js", import.meta.url), "utf8");
const css = await readFile(new URL("../css/chat.css", import.meta.url), "utf8");
const html = await readFile(new URL("../chat.html", import.meta.url), "utf8");

test("Chat has fence-only copy and documents composer keys", () => {
  assert.doesNotMatch(chat, /Copy message|messageCopy|assistantCopyText/);
  assert.match(chat, /Enter sends · Shift\+Enter newline/);
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
	assert.match(chat, /session\?\.pending_approval \? "waiting for you"/);
	assert.match(chat, /pendingApproval\.hidden = !session\?\.pending_approval/);
	assert.match(css, /\.pending-approval\[hidden\]\s*\{\s*display:\s*none/);
	assert.match(css, /#chat-status\.waiting\s*\{\s*color:\s*var\(--alarm\)/);
});

test("Composer sends during an active run and reports projected queue count", () => {
	assert.doesNotMatch(chat, /Run in progress|queue_depth/);
	assert.match(chat, /send\.onclick = submit/);
	assert.match(chat, /queued \? `queued \(\$\{queued\}\)`/);
});

test("New, list, and close are Chat lifecycle controls while Clear is absent", () => {
  assert.match(html, /id="chat-new"/);
  assert.match(html, /id="chat-list-toggle"/);
  assert.match(html, /id="chat-close"/);
  assert.doesNotMatch(html, /chat-clear-conversation|Clear conversation/);
  assert.match(chat, /source_session_id: source\.id/);
  assert.match(chat, /Stop it before closing the chat/);
});

test("Reduced motion remains zero-duration", () => {
  assert.match(css, /prefers-reduced-motion:\s*reduce[\s\S]*animation-duration:\s*0ms\s*!important/);
});
