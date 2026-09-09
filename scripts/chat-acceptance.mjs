import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createServer } from "node:http";
import { mkdir, readFile, readdir, writeFile } from "node:fs/promises";
import { basename, join } from "node:path";
import { chromium } from "playwright";

const args = Object.fromEntries(Array.from({ length: Math.floor(process.argv.slice(2).length / 2) }, (_, index) => {
  const offset = index * 2 + 2;
  return [process.argv[offset].replace(/^--/, ""), process.argv[offset + 1]];
}));
for (const key of ["app", "data", "workspace", "evidence"]) assert.ok(args[key], `missing --${key}`);
const realModel = !!args["real-model-url"];
const startedAt = Date.now();
const scenarios = [];
const children = [];
let browser;
let edgeContext;
let page;
let app;
let model;
let modelPort;
let releaseQueue = null;
let releaseBusy = null;
let slowAccountingArmed = false;
let slowAccountingSkips = 0;
const terminateChildren = () => {
  try { model?.closeAllConnections?.(); } catch {}
  for (const child of [...children].reverse()) { try { child.kill(); } catch {} }
};
process.on("exit", terminateChildren);

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const waitForChildExit = (child, timeout = 5000) => child?.exitCode !== null
  ? Promise.resolve()
  : Promise.race([new Promise((resolve) => child.once("exit", resolve)), sleep(timeout)]);
const record = (name) => { scenarios.push(name); process.stdout.write(`PASS ${name}\n`); };
const freePort = async () => {
  const probe = createServer();
  await new Promise((resolve) => probe.listen(0, "127.0.0.1", resolve));
  const port = probe.address().port;
  await new Promise((resolve) => probe.close(resolve));
  return port;
};
const stream = (response, delta, finish = "stop") => {
  response.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
  response.end(`data: ${JSON.stringify({ choices: [{ delta, finish_reason: finish }], usage: { prompt_tokens: response.agentbPromptTokens || 120, completion_tokens: 12, prompt_tokens_details: { cached_tokens: 80 } } })}\n\ndata: [DONE]\n\n`);
};
const latestUser = (body) => [...(body.messages || [])].reverse().find((message) => message.role === "user")?.content || "";
const hasToolAfterLatestUser = (body) => {
  const messages = body.messages || [];
  const index = messages.findLastIndex((message) => message.role === "user");
  return messages.slice(index + 1).some((message) => message.role === "tool");
};
const toolCountAfterLatestUser = (body) => {
  const messages = body.messages || [];
  const index = messages.findLastIndex((message) => message.role === "user");
  return messages.slice(index + 1).filter((message) => message.role === "tool").length;
};
const fakeHandler = async (request, response) => {
  if (request.url === "/arm-slow-accounting") {
    slowAccountingArmed = true;
    slowAccountingSkips = 1;
    return void response.end(JSON.stringify({ armed: true }));
  }
  if (request.url === "/props") return void response.end(JSON.stringify({ server: "agentb-fake", n_ctx: 32768 }));
  if (request.url === "/v1/models") return void response.end(JSON.stringify({ data: [{ id: "agentb-fake" }] }));
  let raw = "";
  for await (const chunk of request) raw += chunk;
  const body = raw ? JSON.parse(raw) : {};
  response.agentbPromptTokens = Math.max(1, Math.ceil(JSON.stringify(body.messages || []).length / 4));
  if (slowAccountingArmed && (request.url === "/tokenize" || request.url === "/apply-template")) {
    if (slowAccountingSkips > 0) {
      slowAccountingSkips--;
    } else {
      slowAccountingArmed = false;
      await sleep(4000);
    }
  }
  if (request.url === "/tokenize") {
    const content = String(body.content || body.prompt || "");
    return void response.end(JSON.stringify({ tokens: Array.from({ length: Math.max(1, Math.ceil(content.length / 4)) }, (_, i) => i) }));
  }
  if (request.url === "/apply-template") return void response.end(JSON.stringify({ prompt: JSON.stringify(body.messages || []) }));
  if (request.url !== "/v1/chat/completions") { response.statusCode = 404; return void response.end(); }
  const user = latestUser(body);
  if (user.includes("Summarize the work so far")) {
    const slowAccounting = (body.messages || []).some((message) => String(message.content || "").includes("acceptance: slow accounting"));
    response.setHeader("Content-Type", "application/json");
    const content = slowAccounting
      ? "acceptance: slow accounting completed read; answer the pending request now."
      : "Earlier acceptance steps completed; keep the stable system and tool prefix.";
    return void response.end(JSON.stringify({ choices: [{ message: { content }, finish_reason: "stop" }], usage: { prompt_tokens: 300, completion_tokens: 18, prompt_tokens_details: { cached_tokens: 200 } } }));
  }
  if (user.includes("acceptance: stop")) return;
	if (user.includes("acceptance: inbox stop") && !hasToolAfterLatestUser(body)) {
		await sleep(500);
		return stream(response, { tool_calls: [{ index: 0, id: "inbox-list", type: "function", function: { name: "list_dir", arguments: JSON.stringify({ path: ".", depth: 1 }) } }] }, "tool_calls");
	}
	if (user.includes("acceptance: inbox stop")) return stream(response, { content: "INBOX STOP was missed." });
  if (user.includes("acceptance: queue leader")) {
    await new Promise((resolve) => { releaseQueue = resolve; response.on("close", resolve); });
    return stream(response, { content: "Queue leader completed." });
  }
  if (user.includes("acceptance: prose stream")) {
    response.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
    response.write(`data: ${JSON.stringify({ choices: [{ delta: { content: "VISIBLE PARTIAL" }, finish_reason: null }] })}\n\n`);
    await sleep(700);
    response.end(`data: ${JSON.stringify({ choices: [{ delta: { content: " COMPLETE" }, finish_reason: "stop" }], usage: { prompt_tokens: response.agentbPromptTokens || 120, completion_tokens: 12, prompt_tokens_details: { cached_tokens: 80 } } })}\n\ndata: [DONE]\n\n`);
    return;
  }
  if (user.includes("acceptance: menu stream")) {
    const count = toolCountAfterLatestUser(body);
    await sleep(400);
    if (count < 8) {
      const path = count < 2 ? "long-tool.txt" : "AGENTS.md";
      return stream(response, { tool_calls: [{ index: 0, id: `menu-stream-${count}`, type: "function", function: { name: "read_file", arguments: JSON.stringify({ path }) } }] }, "tool_calls");
    }
    return stream(response, { content: "Menu stream completed." });
  }
  if (user.includes("acceptance: busy")) {
    await new Promise((resolve) => { releaseBusy = resolve; response.on("close", resolve); });
    return stream(response, { content: "Busy model resumed." });
  }
  if (user.includes("inspect acceptance directory") && !hasToolAfterLatestUser(body)) {
    return stream(response, { tool_calls: [{ index: 0, id: "acceptance-shell", type: "function", function: { name: "shell", arguments: JSON.stringify({ command: `& "${gitPath}" status --short` }) } }] }, "tool_calls");
  }
  if (user.includes("inspect acceptance directory")) return stream(response, { content: "Acceptance answer rendered after the approved shell call." });
  if (user.includes("acceptance: queued follower")) return stream(response, { content: "Queued follower completed." });
  if (user.includes("acceptance: attachment")) return stream(response, { content: "Attachment received and rendered." });
  if (user.includes("acceptance: recovered")) return stream(response, { content: "Recovered after Retry." });
  if (user.includes("acceptance: slow accounting completed read")) return stream(response, { content: "Slow accounting recovered with an estimate." });
  if (user.includes("acceptance: slow accounting") && !hasToolAfterLatestUser(body)) {
    await sleep(700);
    return stream(response, { tool_calls: [{ index: 0, id: "slow-read", type: "function", function: { name: "read_file", arguments: JSON.stringify({ path: "AGENTS.md" }) } }] }, "tool_calls");
  }
  if (user.includes("acceptance: slow accounting")) { await sleep(700); return stream(response, { content: "Slow accounting recovered with an estimate." }); }
  if (user.includes("acceptance: compaction")) return stream(response, { content: `Compaction answer ${"stable ".repeat(180)}` });
  return stream(response, { content: "Acceptance response." });
};
const startFake = async (port = 0) => {
  model = createServer((request, response) => void fakeHandler(request, response).catch((error) => { response.statusCode = 500; response.end(error.stack); }));
  await new Promise((resolve) => model.listen(port, "127.0.0.1", resolve));
  modelPort = model.address().port;
};
const stopFake = async () => {
  if (!model) return;
  const closing = new Promise((resolve) => model.close(resolve));
  model.closeAllConnections?.();
  await closing;
  model = null;
};
const json = async (url, options) => {
  const response = await fetch(url, options);
  const value = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(`${response.status} ${JSON.stringify(value)}`);
  return value;
};
const waitHTTP = async (url, timeout = 15000) => {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    try { return await json(url); } catch { await sleep(50); }
  }
  throw new Error(`timed out waiting for ${url}`);
};
const waitFileContains = async (path, text, timeout = 12000) => {
	const deadline = Date.now() + timeout;
	while (Date.now() < deadline) {
		try { if ((await readFile(path, "utf8")).includes(text)) return; } catch {}
		await sleep(50);
	}
	throw new Error(`file timeout: ${path} did not contain ${text}`);
};

const browserText = async (selector) => browser.evaluate(`document.querySelector(${JSON.stringify(selector)})?.innerText || ""`);
const projectedChatText = async (sessionID) => JSON.stringify((await state()).sessions[sessionID]?.chat || []);
const waitProjectedChatText = async (sessionID, text, label, timeout = 12000) => {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if ((await projectedChatText(sessionID)).includes(text)) return;
    await sleep(50);
  }
  throw new Error(`projection timeout: ${label}`);
};
const setTask = async (text) => {
  await page.locator("#chat-task").fill(text);
  await page.locator("#chat-send").click();
};
const clickText = async (selector, text) => {
  const exact = new RegExp(`^${text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`);
  await page.locator(selector).filter({ hasText: exact }).click();
  return true;
};
const state = () => json(`http://127.0.0.1:${appPort}/api/state`);
const sessionEvents = async (sessionID) => {
  const files = (await readdir(join(args.data, "logs"))).filter((name) => name.endsWith(".jsonl"));
  const values = [];
  for (const file of files) {
    const lines = (await readFile(join(args.data, "logs", file), "utf8")).split(/\r?\n/).filter(Boolean);
    for (const line of lines) {
      const event = JSON.parse(line);
      if (!sessionID || event.session_id === sessionID) values.push(event);
    }
  }
  return values;
};
const waitEvent = async (sessionID, predicate, label, timeout = 12000) => {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const found = (await sessionEvents(sessionID)).find(predicate);
    if (found) return found;
    await sleep(50);
  }
  throw new Error(`JSONL timeout: ${label}`);
};

const appPort = await freePort();
const gitPath = spawnSync("where.exe", ["git.exe"], { encoding: "utf8" }).stdout.split(/\r?\n/).find(Boolean);
assert.ok(gitPath, "Git is required for the Run as you acceptance scenario");
const bound = join(args.workspace, "..", "acceptance-bound");
await mkdir(bound, { recursive: true });
spawnSync("git.exe", ["init", "--quiet", bound], { stdio: "inherit" });
const attachment = join(args.workspace, "acceptance-attachment.txt");
await writeFile(attachment, "attachment acceptance bytes\n");
await mkdir(join(args.data, "attachments"), { recursive: true });
await writeFile(join(args.data, "attachments", "phone-note.txt"), "operator attachment bytes\n");
await writeFile(join(bound, "AGENTS.md"), "Use the acceptance rules.\n");
await writeFile(join(bound, "long-tool.txt"), Array.from({ length: 100 }, (_, index) => `tool detail line ${index + 1}`).join("\n"));
if (!realModel) await startFake();
const profileURL = realModel ? args["real-model-url"] : `http://127.0.0.1:${modelPort}`;
const profileName = realModel ? args["real-model-name"] : "agentb-fake";
const toolset = ["read_file", "list_dir", "write_file", "edit_file", "search_text", "shell", "remember", "recall", "fetch_url", "find_files", "run_script", "call_service"];
const config = {
  config_version: 6, listen: `127.0.0.1:${appPort}`, workspace: args.workspace, log_dir: join(args.data, "logs"),
  servers: [{ id: "acceptance", label: "Acceptance", base_url: profileURL, model: profileName, credential: "", request_timeout_s: 3, probe_mode: "off",
    sampling: { thinking: { temperature: .6, top_p: .95, top_k: 20, min_p: 0, presence_penalty: 0, repeat_penalty: 1 }, nonthinking: { temperature: .7, top_p: .8, top_k: 20, min_p: 0, presence_penalty: 0, repeat_penalty: 1 } },
    reasoning: { control: "auto", enabled: false, effort: "medium", valid_efforts: [], preserve: false }, context: { n_ctx: 32768, reserve_output: 10240 }, system_prompt_override: "",
    capabilities: { server: "agentb-fake", props: true, n_ctx: 32768, tokenize: true, apply_template: true, apply_template_tools: true, streaming: true, tool_calls: true, grammar_constrained: false, cached_tokens: true, timings: false, prompt_progress: false, document_input: false, image_input: false, reasoning_control: "", valid_efforts: [], overflow_behavior: "error", probed_at: new Date().toISOString(), findings: ["acceptance fake"] } }],
  services: {}, agents: [{ name: "Acceptance", b: "acceptance", toolset }], chat: { auto_rename: false },
  run: { max_turns: 12, cycle_window: 8, max_consecutive_tool_errors: 3, max_concurrent: 2, queue_depth: 0 }, approval: { mode: "boundary-only" },
  deliver: { mode: "chips", exchange_folder: join(args.workspace, "exchange") }, context: { soft_pct: .75, summary_pct: .85, accounting: "auto" }, memory: { enabled: false, dir: join(args.data, "memory"), max_tokens: 1500 },
	operator_files: { allow_mailbox_approvals: false, log_retention_days: 30 },
  tools: { read_file: { default_limit: 16384, max_limit: 65536 }, attachments: { max_bytes: 8388608 }, list_dir: { max_entries: 300, ignore: [".git"] }, grep: { max_matches: 50, max_line_chars: 200 }, shell: { operator_commands: [gitPath] }, fetch: { timeout_s: 20, max_bytes: 2097152, max_redirects: 5, default_limit: 16384, max_limit: 65536, allow_domains: [], deny_domains: [], allow_internal_hosts: [] }, find_files: { skip_roots: [] } },
  shell: { command: ["powershell", "-NoProfile", "-NonInteractive", "-Command"], timeout_s: 60, max_timeout_s: 600, max_output_lines_head: 60, max_output_lines_tail: 40, file_routing_guard: true, operator_context: false, operator_context_idle_timeout_minutes: 20, service_account: { enabled: true, account: "agentb-svc", domain: "." }, deny: [] },
  signing: { thumbprint: "", timestamp_url: "http://timestamp.digicert.com" }
};
await writeFile(join(args.data, "harness.json"), JSON.stringify(config, null, 2));
app = spawn(join(args.app, "Agent_b.exe"), ["-config", join(args.data, "harness.json"), "-app-root", args.app, "-data-root", args.data], { windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
children.push(app);
app.stdout.on("data", (chunk) => process.stdout.write(chunk));
app.stderr.on("data", (chunk) => process.stderr.write(chunk));
await waitHTTP(`http://127.0.0.1:${appPort}/api/state`);
const loadedConfig = await json(`http://127.0.0.1:${appPort}/api/config`);
assert.equal(loadedConfig.shell?.service_account?.enabled, true, "disposable install must exercise split identity");
assert.equal(loadedConfig.servers?.[0]?.request_timeout_s, 3, "slow-accounting fixture needs a three-second request timeout");
assert.equal(loadedConfig.servers?.[0]?.capabilities?.tokenize, true, "slow-accounting fixture needs exact tokenization");
assert.equal(loadedConfig.context?.accounting, "auto", "slow-accounting fixture needs automatic exact accounting");
edgeContext = await chromium.launchPersistentContext("", {
  channel: "msedge",
  headless: false,
  viewport: { width: 1250, height: 975 },
  args: [`--app=http://127.0.0.1:${appPort}/chat`, "--window-size=1250,975"],
});
page = edgeContext.pages()[0] || await edgeContext.newPage();
page.on("pageerror", (error) => process.stderr.write(`PAGE ERROR: ${error.stack || error}\n`));
browser = {
  evaluate: (expression) => page.evaluate(expression),
  wait: async (expression, label, timeout = 12000) => {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      if (await page.evaluate(`Boolean(${expression})`)) return;
      await sleep(50);
    }
    throw new Error(`screen timeout: ${label}`);
  },
};
await page.goto(`http://127.0.0.1:${appPort}/chat`);
await browser.wait(`document.querySelector('#chat-task')`, "Chat opened");
await browser.wait(`document.querySelector('.agent-tab')`, "Agent tab rendered");
record("open-chat");

if (realModel) {
  await setTask("Reply with the exact words REAL MODEL ACCEPTANCE OK.");
  await waitProjectedChatText((await state()).active, "REAL MODEL ACCEPTANCE OK", "real model answer", 120000);
  record("real-model-answer");
} else {
  await page.locator(".agent-tab-new").click();
  await page.locator("button.shell-new-choice").filter({ hasText: /^Default ·/ }).click();
  let snapshot;
  await browser.wait(`new URLSearchParams(location.search).get('session')?.startsWith('s')`, "new session selected");
  record("agent-tab-new-chat-idle");
  snapshot = await state();
  const sessionID = await browser.evaluate(`new URLSearchParams(location.search).get('session')`);
  const session = snapshot.sessions[sessionID];
  assert.equal(session?.id, sessionID, "selected new chat must exist in the server snapshot");
  record("new-chat");

  await page.locator("#chat-task").fill("acceptance: prose stream");
  await page.locator("#chat-send").click();
  await waitProjectedChatText(sessionID, "VISIBLE PARTIAL", "mid-stream prose partial");
  await browser.wait(`document.querySelector('.chat-response-prose')?.innerText.includes('VISIBLE PARTIAL')`, "partial prose visible without expansion");
  const partialProse = await browser.evaluate(`(() => ({
    text: document.querySelector('.chat-response-prose')?.innerText || '',
    turnExpanded: document.querySelector('.chat-response-summary')?.getAttribute('aria-expanded'),
    caret: document.querySelector('.chat-response-prose .stream-caret')?.isConnected || false
  }))()`);
  assert.match(partialProse.text, /VISIBLE PARTIAL/);
  assert.equal(partialProse.turnExpanded, "true");
  assert.equal(partialProse.caret, true);
  await waitProjectedChatText(sessionID, "VISIBLE PARTIAL COMPLETE", "completed prose stream");
  record("mid-stream-prose-visible-without-expansion");

  const baselineDirectory = join(args.evidence, "baseline-initial");
  await mkdir(baselineDirectory, { recursive: true });
  await page.screenshot({ path: join(baselineDirectory, "chat-idle.png") });
  await page.locator(".agent-tab").first().click({ button: "right" });
  await page.locator(".agent-chat-count").waitFor({ state: "visible" });
  await page.locator(`.agent-chat-row[data-session="${sessionID}"] .agent-chat-open`).waitFor({ state: "visible" });
  const initialMenuRows = await page.locator(".agent-chat-row").count();
  assert.match(await page.locator(".agent-chat-count").innerText(), new RegExp(`^${initialMenuRows} chats? · ${initialMenuRows} open · 0 closed$`));
  await page.screenshot({ path: join(baselineDirectory, "tab-menu-open.png") });
  await page.goto(`http://127.0.0.1:${appPort}/?session=${sessionID}`);
  await page.locator("#console-lifetime").waitFor({ state: "visible" });
  await page.screenshot({ path: join(baselineDirectory, "console.png") });
  await page.locator(".shell-settings").click();
  await page.locator("#settings-page").waitFor({ state: "visible" });
  await page.screenshot({ path: join(baselineDirectory, "settings.png") });
  await page.goto(`http://127.0.0.1:${appPort}/plan?session=${sessionID}`);
  await page.locator('#app-shell[data-page="plan"]').waitFor({ state: "visible" });
  await page.screenshot({ path: join(baselineDirectory, "plan.png") });
  await page.goto(`http://127.0.0.1:${appPort}/chat?session=${sessionID}`);
  await page.locator("#chat-task").waitFor({ state: "visible" });

  await page.locator("#chat-task").fill("acceptance: menu stream");
  await page.locator("#chat-send").click();
  const lifecycleRunStarted = await waitEvent(sessionID, (event) => event.type === "run.started", "tool-tick lifecycle run started");
  await waitProjectedChatText(sessionID, "menu-stream-0", "first projected lifecycle tool");
  const lifecycleTurn = page.locator(".chat-response-summary").last();
  await lifecycleTurn.waitFor({ state: "visible" });
  if (await lifecycleTurn.getAttribute("aria-expanded") !== "true") await lifecycleTurn.click();
  assert.equal(await page.locator(".chat-tool-group-head").count(), 0, "active responses must not regroup live tool nodes");
  await page.locator("button.tool-tick").first().waitFor({ state: "visible" });
  const toolButton = page.locator("button.tool-tick").first();
  await toolButton.waitFor({ state: "visible" });
  await toolButton.hover();
  const toolButtonHandle = await toolButton.elementHandle();
  assert.ok(toolButtonHandle, "tool-tick must have an actionable node");
  assert.equal(await toolButtonHandle.evaluate((node) => node.matches(":hover")), true, "tool-tick must be hovered before the event stream advances");
  await page.waitForTimeout(250);
  const toolButtonAfterBeat = await toolButtonHandle.evaluate((node) => ({ attached: node.isConnected, hovered: node.matches(":hover") }));
  assert.equal(toolButtonAfterBeat.attached, true, "tool-tick node changed during the active event stream");
  assert.equal(toolButtonAfterBeat.hovered, true, "tool-tick lost :hover during the active event stream");
  await toolButtonHandle.click();
  assert.equal(await toolButtonHandle.getAttribute("aria-expanded"), "true", "tool-tick did not expand from a trusted mid-stream click");
  const toolRoot = page.locator("button.tool-tick").first().locator("..");
  const collapseArrow = toolRoot.locator("button.collapse-arrow");
  await collapseArrow.waitFor({ state: "visible" });
  await page.screenshot({ path: join(baselineDirectory, "chat-mid-run.png") });
  await toolRoot.evaluate((root) => { root.style.minHeight = "1200px"; });
  await page.evaluate(() => {
    const spacer = document.createElement("div");
    spacer.dataset.acceptanceSpacer = "collapse-arrow";
    spacer.style.height = "1200px";
    document.querySelector("#chat-log")?.append(spacer);
  });
  const arrowBeforeScroll = await collapseArrow.boundingBox();
  assert.ok(arrowBeforeScroll, "collapse arrow must have a visible box after expansion");
  await page.mouse.wheel(0, 400);
  await page.waitForTimeout(50);
  const arrowPinned = await collapseArrow.boundingBox();
  await page.mouse.wheel(0, 200);
  await page.waitForTimeout(50);
  const arrowDuringScroll = await collapseArrow.boundingBox();
  const arrowTrackingState = await collapseArrow.evaluate((node) => {
    const log = document.querySelector("#chat-log");
    const section = node.parentElement;
    const style = getComputedStyle(node);
    return { scrollTop: log?.scrollTop, log: log?.getBoundingClientRect().toJSON(), section: section?.getBoundingClientRect().toJSON(), position: style.position, top: style.top, float: style.cssFloat };
  });
  assert.ok(arrowPinned && arrowDuringScroll && Math.abs(arrowDuringScroll.y - arrowPinned.y) < 8, `collapse arrow must track while its section remains on screen: ${JSON.stringify({ arrowBeforeScroll, arrowPinned, arrowDuringScroll, arrowTrackingState })}`);
  await page.mouse.wheel(0, 5000);
  await page.waitForTimeout(50);
  const arrowAfterSection = await collapseArrow.boundingBox();
  assert.ok(!arrowAfterSection || arrowAfterSection.y < 0 || arrowAfterSection.y > 975, "collapse arrow must leave the viewport with its section");
  await page.evaluate(() => document.querySelector('[data-acceptance-spacer="collapse-arrow"]')?.remove());
  await toolRoot.evaluate((root) => { root.style.minHeight = ""; });
  await toolButton.scrollIntoViewIfNeeded();
  await collapseArrow.click();
  assert.equal(await toolButtonHandle.getAttribute("aria-expanded"), "false", "collapse arrow must collapse its own tool section");
  await collapseArrow.waitFor({ state: "hidden" });
  record("tool-tick-node-lifecycle-active-run");
  await page.locator("#chat-stop").click();
  await waitEvent(sessionID, (event) => event.type === "run.stopped" && event.seq > lifecycleRunStarted.seq, "tool-tick lifecycle run stopped");

  const missingArgsFixture = await browser.evaluate(`(async () => {
    const bus = await import('/static/js/bus.js');
    const session = bus.store.sessions[${JSON.stringify(sessionID)}];
    session.chat = [
      { type: 'user', key: 'hotfix:user', text: 'before malformed tool' },
      { type: 'tool', key: 'hotfix:missing-args', name: 'read_file' },
      { type: 'agent', key: 'hotfix:answer', text: 'after malformed tool', done: true }
    ];
    bus.setSelection('agent_b', ${JSON.stringify(sessionID)});
    await new Promise(resolve => setTimeout(resolve, 120));
    const summary = document.querySelector('.chat-response-summary');
    const collapsed = summary?.innerText || '';
    summary?.click();
    await new Promise(resolve => setTimeout(resolve, 120));
    return {
      collapsed,
      rows: document.querySelectorAll('[data-entry-key]').length,
      responseAlarm: summary?.closest('.chat-response')?.classList.contains('alarm') || false,
      failureAlarm: document.querySelector('.chat-render-failure')?.classList.contains('alarm') || false,
      text: document.querySelector('#chat-log')?.innerText || ''
    };
  })()`);
  assert.doesNotMatch(missingArgsFixture.collapsed, /failed/);
  assert.equal(missingArgsFixture.rows, 3);
  assert.equal(missingArgsFixture.responseAlarm, false);
  assert.equal(missingArgsFixture.failureAlarm, false);
  assert.match(missingArgsFixture.text, /tool read_file could not render · tool arguments are missing or are not an object/);
  assert.match(missingArgsFixture.text, /before malformed tool/);
  assert.match(missingArgsFixture.text, /after malformed tool/);
  record("missing-tool-args-isolated");

  let events = await sessionEvents(sessionID);
  const beforeRenderFailure = events.at(-1)?.seq || 0;
  const throwingFixture = await browser.evaluate(`(async () => {
    const bus = await import('/static/js/bus.js');
    const session = bus.store.sessions[${JSON.stringify(sessionID)}];
    const args = new Proxy({}, { ownKeys() { throw new Error('deliberate render failure'); } });
    session.chat = [
      { type: 'user', key: 'hotfix:kept', text: 'other entry remains' },
      { type: 'tool', key: 'hotfix:throwing', name: 'shell', args }
    ];
    for (let index = 0; index < 32; index++) {
      bus.setSelection('agent_b', ${JSON.stringify(sessionID)});
      await new Promise(resolve => setTimeout(resolve, 65));
      if (index === 0) document.querySelector('.chat-response-summary')?.click();
    }
    const failure = document.querySelector('.chat-render-failure');
    return {
      text: document.querySelector('#chat-log')?.innerText || '',
      responseAlarm: document.querySelector('.chat-response')?.classList.contains('alarm') || false,
      failureAlarm: failure?.classList.contains('alarm') || false
    };
  })()`);
  assert.match(throwingFixture.text, /tool shell could not render · deliberate render failure/);
  assert.match(throwingFixture.text, /other entry remains/);
  assert.doesNotMatch(throwingFixture.text, /No agent connected/);
  assert.equal(throwingFixture.responseAlarm, false);
  assert.equal(throwingFixture.failureAlarm, false);
  await waitEvent(sessionID, (event) => event.type === "error" && event.seq > beforeRenderFailure && event.data?.where === "ui" && event.data?.capped === true, "capped UI render failure", 6000);
  events = await sessionEvents(sessionID);
  const relayedRenderFailures = events.filter((event) => event.type === "error" && event.seq > beforeRenderFailure && event.data?.where === "ui" && event.data?.message?.includes("tool shell deliberate render failure"));
  assert.equal(relayedRenderFailures.length, 2, JSON.stringify(relayedRenderFailures.map((event) => event.data)));
  assert.deepEqual(relayedRenderFailures.map((event) => [event.data.repeat_count, event.data.capped]), [[1, false], [25, true]]);
  record("render-failure-empty-state-and-bounded-relay-2");

  await browser.evaluate(`(async () => { const bus = await import('/static/js/bus.js'); bus.reduce({ type: 'snapshot', data: await fetch('/api/state', { cache: 'no-store' }).then(response => response.json()) }); return true; })()`);
  await browser.wait(`document.querySelector('#chat-log') && !document.querySelector('#chat-log').innerText.includes('deliberate render failure')`, "server snapshot restored");

  const groupingFixture = await browser.evaluate(`(async () => {
    const bus = await import('/static/js/bus.js');
    const session = bus.store.sessions[${JSON.stringify(sessionID)}];
    session.run = { ...session.run, status: 'idle' };
    session.chat = [
      { type: 'user', key: 'group:user', text: 'group every recorded row' },
      { type: 'tool', key: 'group:read-1', name: 'read_file', args: { path: 'one.txt' }, content: 'ONE COMPLETE', result: { ok: true, ms: 4 } },
      { type: 'agent', key: 'group:thin', reasoning: 'thin recorded thought', reasoningTokens: 12, thinkingMS: 3, done: true },
      { type: 'tool', key: 'group:read-2', name: 'read_file', args: { path: 'two.txt' }, content: 'TWO COMPLETE FAILURE', result: { ok: false, ms: 5 } },
      { type: 'agent', key: 'group:long', reasoning: 'long recorded thought', reasoningTokens: 65, thinkingMS: 7, done: true },
      { type: 'tool', key: 'group:read-3', name: 'read_file', args: { path: 'three.txt' }, content: 'THREE COMPLETE', result: { ok: true, ms: 6 } }
    ];
    bus.setSelection('agent_b', ${JSON.stringify(sessionID)});
    await new Promise(resolve => setTimeout(resolve, 120));
    const summary = document.querySelector('.chat-response-summary');
    const collapsed = summary?.innerText || '';
    const collapsedRows = document.querySelectorAll('.chat-response-rows > *').length;
    summary?.click();
    await new Promise(resolve => setTimeout(resolve, 120));
    const group = document.querySelector('.chat-tool-group-head');
    const rows = document.querySelectorAll('.chat-step-rows > *').length;
    const groupText = group?.innerText || '';
    group?.click();
    await new Promise(resolve => setTimeout(resolve, 120));
    document.querySelector('[data-entry-key="group:long"] .thinking-line')?.click();
    document.querySelector('[data-entry-key="group:read-3"] .tool-tick')?.click();
    await new Promise(resolve => setTimeout(resolve, 120));
    return {
      collapsed, collapsedRows, rows, groupText,
      calls: document.querySelectorAll('.chat-tool-group-calls .tool-tick').length,
      details: document.querySelectorAll('.chat-tool-group-calls .tool-detail').length,
      text: document.querySelector('#chat-log')?.innerText || ''
    };
  })()`);
  assert.match(groupingFixture.collapsed, /3 tool calls · 1 failed · 2 thoughts · 25 ms/);
  assert.equal(groupingFixture.collapsedRows, 1);
  assert.equal(groupingFixture.rows, 3);
  assert.match(groupingFixture.groupText, /read_file ×2 · \+1 thought · 1 failed · 12 ms/);
  assert.equal(groupingFixture.calls, 2);
  assert.equal(groupingFixture.details, 2);
  for (const text of ["ONE COMPLETE", "thin recorded thought", "TWO COMPLETE FAILURE", "long recorded thought", "THREE COMPLETE"]) assert.match(groupingFixture.text, new RegExp(text));
  record("three-level-chat-fold-adjacent-thin-failure-complete");
  await browser.evaluate(`(async () => { const bus = await import('/static/js/bus.js'); bus.reduce({ type: 'snapshot', data: await fetch('/api/state', { cache: 'no-store' }).then(response => response.json()) }); return true; })()`);
  await browser.wait(`document.querySelector('#chat-log') && !document.querySelector('#chat-log').innerText.includes('TWO COMPLETE FAILURE')`, "grouping fixture restored");

  const proseBlocksFixture = await browser.evaluate(`(async () => {
    const bus = await import('/static/js/bus.js');
    const session = bus.store.sessions[${JSON.stringify(sessionID)}];
    session.run = { ...session.run, status: 'idle' };
    session.chat = [
      { type: 'user', key: 'prose:user', text: 'keep every prose block visible' },
      { type: 'agent', key: 'prose:first', text: 'FIRST PROSE BLOCK', reasoning: 'FIRST PRIVATE THOUGHT', reasoningTokens: 8, done: true },
      { type: 'tool', key: 'prose:first-tool', name: 'read_file', args: { path: 'first.txt' }, content: 'FIRST TOOL RESULT', result: { ok: true, ms: 4 } },
      { type: 'notice', key: 'prose:first-notice', event: { type: 'compaction', data: { before: 20, after: 10 } } },
      { type: 'agent', key: 'prose:second', text: 'SECOND PROSE BLOCK', reasoning: 'SECOND PRIVATE THOUGHT', reasoningTokens: 9, done: true },
      { type: 'tool', key: 'prose:second-tool', name: 'shell', args: { command: 'echo second' }, content: 'SECOND TOOL RESULT', result: { ok: true, ms: 5 } }
    ];
    bus.setSelection('agent_b', ${JSON.stringify(sessionID)});
    await new Promise(resolve => setTimeout(resolve, 120));
    const response = document.querySelector('.chat-response');
    const turn = response.querySelector('.chat-response-summary');
    let folds = [...response.querySelectorAll('.chat-step-summary')];
    let prose = [...response.querySelectorAll('.chat-response-prose')];
    const firstProse = prose[0];
    const initial = {
      prose: prose.map(node => node.innerText),
      foldCount: folds.length,
      open: folds.map(node => node.getAttribute('aria-expanded')),
      stepRows: [...response.querySelectorAll('.chat-step-rows')].map(node => node.children.length)
    };
    folds[0].click();
    await new Promise(resolve => setTimeout(resolve, 80));
    folds = [...response.querySelectorAll('.chat-step-summary')];
    const afterFirst = {
      open: folds.map(node => node.getAttribute('aria-expanded')),
      first: folds[0].nextElementSibling.innerText,
      firstKeys: [...folds[0].nextElementSibling.querySelectorAll('[data-entry-key]')].map(node => node.dataset.entryKey),
      secondRows: folds[1].nextElementSibling.children.length,
      proseStable: firstProse === response.querySelectorAll('.chat-response-prose')[0] && firstProse.isConnected
    };
    turn.click();
    await new Promise(resolve => setTimeout(resolve, 80));
    folds = [...response.querySelectorAll('.chat-step-summary')];
    const afterTurnOpen = folds.map(node => node.getAttribute('aria-expanded'));
    const afterTurnOpenKeys = folds.map(node => [...node.nextElementSibling.querySelectorAll('[data-entry-key]')].map(row => row.dataset.entryKey));
    turn.click();
    await new Promise(resolve => setTimeout(resolve, 80));
    folds = [...response.querySelectorAll('.chat-step-summary')];
    prose = [...response.querySelectorAll('.chat-response-prose')];
    return {
      initial,
      afterFirst,
      afterTurnOpen,
      afterTurnOpenKeys,
      afterTurnClose: folds.map(node => node.getAttribute('aria-expanded')),
      finalProse: prose.map(node => node.innerText),
      proseStable: firstProse === prose[0] && firstProse.isConnected,
      secondCollapsedText: folds[1].nextElementSibling.innerText
    };
  })()`);
  assert.deepEqual(proseBlocksFixture.initial.prose, ["FIRST PROSE BLOCK", "SECOND PROSE BLOCK"]);
  assert.equal(proseBlocksFixture.initial.foldCount, 2);
  assert.deepEqual(proseBlocksFixture.initial.open, ["false", "false"]);
  assert.deepEqual(proseBlocksFixture.initial.stepRows, [0, 0]);
  assert.deepEqual(proseBlocksFixture.afterFirst.open, ["true", "false"]);
  assert.equal(proseBlocksFixture.afterFirst.secondRows, 0);
  assert.deepEqual(proseBlocksFixture.afterFirst.firstKeys, ["thought:prose:first", "prose:first-tool", "prose:first-notice"]);
  assert.match(proseBlocksFixture.afterFirst.first, /compacted −10 tokens/);
  assert.equal(proseBlocksFixture.afterFirst.proseStable, true);
  assert.deepEqual(proseBlocksFixture.afterTurnOpen, ["true", "true"]);
  assert.deepEqual(proseBlocksFixture.afterTurnOpenKeys, [
    ["thought:prose:first", "prose:first-tool", "prose:first-notice"],
    ["thought:prose:second", "prose:second-tool"]
  ]);
  assert.deepEqual(proseBlocksFixture.afterTurnClose, ["false", "false"]);
  assert.deepEqual(proseBlocksFixture.finalProse, ["FIRST PROSE BLOCK", "SECOND PROSE BLOCK"]);
  assert.equal(proseBlocksFixture.proseStable, true);
  assert.equal(proseBlocksFixture.secondCollapsedText, "");
  await page.setViewportSize({ width: 320, height: 720 });
  const narrowProse = await browser.evaluate(`(() => {
    const response = document.querySelector('.chat-response');
    const prose = response.querySelector('.chat-response-prose').getBoundingClientRect();
    const fold = response.querySelector('.chat-step-fold').getBoundingClientRect();
    const log = document.querySelector('#chat-log');
    return {
      inset: fold.left - prose.left,
      foldRight: fold.right,
      proseRight: prose.right,
      logOverflow: log.scrollWidth - log.clientWidth,
      pageOverflow: document.documentElement.scrollWidth - document.documentElement.clientWidth
    };
  })()`);
  assert.ok(narrowProse.inset >= 4 && narrowProse.foldRight <= narrowProse.proseRight + 0.5, JSON.stringify(narrowProse));
  assert.ok(narrowProse.logOverflow <= 0 && narrowProse.pageOverflow <= 0, JSON.stringify(narrowProse));
  await page.setViewportSize({ width: 1250, height: 975 });
  record("prose-always-visible-independent-step-folds-no-horizontal-scroll");
  await browser.evaluate(`(async () => { const bus = await import('/static/js/bus.js'); bus.reduce({ type: 'snapshot', data: await fetch('/api/state', { cache: 'no-store' }).then(response => response.json()) }); return true; })()`);
  await browser.wait(`document.querySelector('#chat-log') && !document.querySelector('#chat-log').innerText.includes('FIRST PROSE BLOCK')`, "prose fixture restored");

  const geometry = await browser.evaluate(`(() => { const textarea=document.querySelector('#chat-task').getBoundingClientRect(); const row=document.querySelector('.chat-composer-row').getBoundingClientRect(); const expand=document.querySelector('#chat-expand').getBoundingClientRect(); const robot=document.querySelector('.agent-tab-wrap[data-agent="agent_b"] .agent-tab-robot').getBoundingClientRect(); const tab=document.querySelector('.agent-tab-wrap[data-agent="agent_b"]').getBoundingClientRect(); const plus=document.querySelector('.agent-tab-new').getBoundingClientRect(); const send=document.querySelector('#chat-send').getBoundingClientRect(); const stop=document.querySelector('#chat-stop').getBoundingClientRect(); return {textarea:textarea.width,row:row.width,rowHeight:row.height,expandTop:expand.top-textarea.top,expandRight:textarea.right-expand.right,robot:robot.width,tab:tab.width,plus:{width:plus.width,height:plus.height},send:{width:send.width,height:send.height},stop:{width:stop.width,height:stop.height}}; })()`);
  assert.ok(geometry.textarea >= geometry.row - 50, JSON.stringify(geometry));
  assert.ok(geometry.expandTop >= 0 && geometry.expandTop <= 8 && geometry.expandRight >= 0 && geometry.expandRight <= 8, JSON.stringify(geometry));
  assert.ok(geometry.robot > 0, JSON.stringify(geometry));
  assert.ok(geometry.tab < 180 && geometry.plus.width === 20 && geometry.plus.height === 20, JSON.stringify(geometry));
  assert.deepEqual(geometry.send, geometry.stop, JSON.stringify(geometry));
  assert.ok(Math.abs(geometry.send.height - 24) < 0.01, JSON.stringify(geometry));
  record("composer-flex-width-expand-robot-tab-plus-equal-controls");

  await setTask(`Please inspect acceptance directory "${bound}" and report.`);
  await browser.wait(`document.querySelector('.workspace-bind-card')?.innerText.includes('Bind this chat to')`, "bind offer");
  await waitEvent(sessionID, (event) => event.type === "workspace.bind_required", "workspace.bind_required");
  assert.equal(await clickText(".workspace-bind-card button", "Yes"), true);
  await waitEvent(sessionID, (event) => event.type === "workspace.bound", "workspace.bound");
  await waitEvent(sessionID, (event) => event.type === "approval.required", "approval.required");
  await page.reload();
  await browser.wait(`performance.getEntriesByType('navigation')[0]?.type==='reload' && document.querySelector('#chat-task')`, "pending approval refresh");
  await waitFileContains(join(args.data, "OUTBOX.md"), "needs you: approval is waiting");
  record("outbox-line-on-pause");
  await browser.wait(`[...document.querySelectorAll('.approval-card')].some(item=>item.innerText.toLowerCase().includes('run as you'))`, "Run as you card");
  assert.equal(await clickText(".approval-card button", "Yes, for this chat"), true);
  await waitProjectedChatText(sessionID, "Acceptance answer rendered after the approved shell call.", "answer rendered");
  events = await sessionEvents(sessionID);
  assert.ok(events.some((event) => event.type === "shell.grant" && event.data.scope === "session"));
  assert.ok(events.some((event) => event.type === "tool.result" && event.data.name === "shell" && event.data.ok === true));
  const gutter = await browser.evaluate(`getComputedStyle(document.querySelector('.chat-entry')).gridTemplateColumns.split(' ')[0]`);
  assert.match(gutter, /^90px$/);
  record("bind-run-as-you-tool-answer-gutter");

  await page.goto(`http://127.0.0.1:${appPort}/?session=${sessionID}`);
  await browser.wait(`location.pathname==='/' && document.querySelector('#settings-page') && document.querySelector('.shell-settings')?.getAttribute('href')`, "Console settings control");
  await page.locator(".shell-settings").click();
  await browser.wait(`!document.querySelector('#settings-page').hidden`, "Settings open");
  assert.equal(await clickText(".settings-nav button", "Workspace"), true);
  await browser.wait(`!document.querySelector('#settings-page').hidden && document.querySelector('.settings-content')?.innerText.includes('Adopt repository instructions')`, "operator-file Workspace settings");
  const workspaceSettings = await browserText(".settings-content");
  for (const text of ["attachments", "Empty", "log retention (days)", "Adopt repository instructions", "Also remove AGENTS.md / CLAUDE.md"]) assert.ok(workspaceSettings.includes(text), `Workspace settings missing ${text}`);
  assert.equal(await browser.evaluate(`document.querySelector('#adopt-instruction-cleanup')?.checked`), false);
  assert.equal(await browser.evaluate(`document.querySelector('[data-path="operator_files.allow_mailbox_approvals"]')?.getAttribute('aria-checked')`), "false");
  assert.equal(await clickText(".settings-content button", "Adopt"), true);
  await waitFileContains(join(bound, "AGENT_B.md"), "Use the acceptance rules.");
  assert.equal(await readFile(join(bound, "AGENTS.md"), "utf8"), "Use the acceptance rules.\n");
  record("workspace-operator-files-and-adopt");
	await page.goto(`http://127.0.0.1:${appPort}/chat?session=${sessionID}`);
	await browser.wait(`document.querySelector('#chat-task')`, "chat restored after settings");
	events = await sessionEvents(sessionID);
	const beforeUIError = events.at(-1)?.seq || 0;
	await browser.evaluate(`(() => { console.error('acceptance UI relay'); return true; })()`);
	await waitEvent(sessionID, (event) => event.type === "error" && event.seq > beforeUIError && event.data?.where === "ui" && event.data?.message?.includes("acceptance UI relay"), "UI error relay");
	record("ui-error-relay-session-jsonl");

  await setTask("acceptance: stop");
  await browser.wait(`!document.querySelector('#chat-stop').disabled`, "stop enabled");
  const stopStart = Date.now();
  await page.locator("#chat-stop").click();
  await browser.wait(`document.querySelector('#chat-stop').disabled`, "stop completed", 1000);
  assert.ok(Date.now() - stopStart < 1000, `Stop took ${Date.now() - stopStart} ms`);
  await waitEvent(sessionID, (event) => event.type === "run.stopped" && event.data.reason === "aborted_mid_model", "stopped run");
  record("stop-under-one-second");

	events = await sessionEvents(sessionID);
	const beforeInboxStop = events.at(-1)?.seq || 0;
	await setTask("acceptance: inbox stop");
	await waitEvent(sessionID, (event) => event.type === "model.request" && event.seq > beforeInboxStop, "inbox-stop model request");
	await writeFile(join(args.data, "INBOX.md"), "STOP\n");
	await waitEvent(sessionID, (event) => event.type === "run.stopped" && event.seq > beforeInboxStop && event.data.reason === "mailbox_stop", "INBOX STOP", 15000);
	assert.equal(await readFile(join(args.data, "INBOX.md"), "utf8"), "");
	await waitFileContains(join(args.data, "OUTBOX.md"), "stopped: STOP read from INBOX.md");
	assert.ok((await browserText("#chat-log")).includes("acceptance: inbox stop"));
	record("inbox-stop-mid-run");

  await setTask("acceptance: queue leader");
  await browser.wait(`!document.querySelector('#chat-stop').disabled`, "queue leader running");
  await setTask("acceptance: queued follower");
  await browser.wait(`document.querySelector('#chat-status-strip')?.innerText.includes('queued (1)')`, "queued count");
  await waitEvent(sessionID, (event) => event.type === "message.queued" && event.data.position === 1, "message.queued");
  releaseQueue?.();
  await waitProjectedChatText(sessionID, "Queued follower completed.", "queued follower answer");
  events = await sessionEvents(sessionID);
  const queueUsers = events.filter((event) => event.type === "message.appended" && event.data.message?.role === "user").map((event) => event.data.message.content);
  assert.ok(queueUsers.indexOf("acceptance: queue leader") < queueUsers.indexOf("acceptance: queued follower"));
  record("active-run-queue-fifo");

  await page.locator("#chat-attach").click();
  await page.locator("#chat-attach-exchange").click();
  await browser.wait(`[...document.querySelectorAll('#chat-exchange-files button')].some(item=>item.innerText.includes('phone-note.txt'))`, "operator attachment listed");
  assert.equal(await clickText("#chat-exchange-files button", "phone-note.txt · 26 B"), true);
  await browser.wait(`document.querySelector('.chat-pending-file')`, "pending attachment");
  await setTask("acceptance: attachment");
  await waitProjectedChatText(sessionID, "Attachment received and rendered.", "attachment answer");
  await waitEvent(sessionID, (event) => event.type === "message.appended" && event.data.message?.attachments?.length === 1, "attachment JSONL");
  record("operator-attachments-paperclip-source");
  record("attachment-screen-jsonl");

  const beforeReload = (await browserText("#chat-log")).slice(0, 120);
  await page.reload();
  await waitProjectedChatText(sessionID, "Attachment received and rendered.", "chat reopen");
  await browser.wait(`document.querySelectorAll('#chat-log .chat-entry').length > 1`, "chat rows restored after reopen");
  assert.equal((await browserText("#chat-log")).slice(0, 120), beforeReload);
  record("chat-reopen-preserves-screen-and-jsonl");

  events = await sessionEvents(sessionID);
  const beforeSlowAccounting = events.at(-1)?.seq || 0;
  await json(`${profileURL}/arm-slow-accounting`, { method: "POST" });
  await setTask(`acceptance: slow accounting ${"payload ".repeat(800)}`);
  const estimatedBudget = await waitEvent(sessionID, (event) => event.type === "budget" && event.seq > beforeSlowAccounting && event.data?.estimated === true, "estimated slow-accounting budget", 12000);
  await browser.wait(`document.querySelector('.chat-budget-tip')?.innerText.includes('estimated')`, "estimated occupancy label");
  const slowStop = await waitEvent(sessionID, (event) => event.type === "run.stopped" && event.seq > estimatedBudget.seq, "slow-accounting run stopped", 20000);
  assert.equal(slowStop.data.reason, "done");
  events = await sessionEvents(sessionID);
  await waitProjectedChatText(sessionID, "Slow accounting recovered with an estimate.", "slow-accounting answer", 20000);
  assert.ok(events.some((event) => event.type === "model.busy" && event.seq > beforeSlowAccounting));
  assert.ok(events.some((event) => event.type === "budget" && event.seq > estimatedBudget.seq && event.data?.estimated === false));
  record("long-run-slow-accounting");

  await stopFake();
  await setTask("acceptance: unreachable");
  await browser.wait(`document.querySelector('#chat-status-strip')?.innerText.includes('model unreachable')`, "unreachable strip");
  await waitEvent(sessionID, (event) => event.type === "model.unreachable", "model.unreachable");
  await setTask("acceptance: recovered");
  await browser.wait(`document.querySelector('#chat-status-strip')?.innerText.includes('queued (1)')`, "recovery queued");
  await startFake(modelPort);
  await page.locator("#chat-retry-model").click();
  await waitProjectedChatText(sessionID, "Recovered after Retry.", "Retry recovery", 20000);
  await waitEvent(sessionID, (event) => event.type === "model.reachable", "model.reachable");
  record("model-unreachable-retry-release");

  await setTask("acceptance: busy");
  await browser.wait(`document.querySelector('#chat-status-strip')?.innerText.includes('model busy')`, "busy strip", 6000);
  events = await sessionEvents(sessionID);
  const busyEvent = events.findLast((event) => event.type === "model.busy");
  assert.ok(busyEvent);
  assert.equal(events.slice(events.indexOf(busyEvent)).some((event) => event.type === "run.stopped"), false);
  releaseBusy?.();
  await waitProjectedChatText(sessionID, "Busy model resumed.", "busy resumed");
  await waitEvent(sessionID, (event) => event.type === "run.stopped" && event.seq > busyEvent.seq, "busy run stopped");
  record("model-busy-waits-without-stop");

  const beforeCompactionSequence = (await sessionEvents(sessionID)).at(-1)?.seq || 0;
  for (let index = 0; index < 12; index++) {
    events = await sessionEvents(sessionID);
    const beforeSequence = events.at(-1)?.seq || 0;
    await setTask(`acceptance: compaction ${index} ${"payload ".repeat(1200)}`);
    await waitEvent(sessionID, (event) => event.type === "run.stopped" && event.seq > beforeSequence, `compaction run ${index}`, 20000);
  }
  const compaction = await waitEvent(sessionID, (event) => event.type === "compaction", "compaction", 20000);
  assert.ok(compaction.data.before > compaction.data.after);
  events = await sessionEvents(sessionID);
  const requests = events.filter((event) => event.type === "model.request" && event.body && event.seq > beforeCompactionSequence);
  const prefix = (event) => JSON.stringify({ system: event.body.messages?.[0], tools: event.body.tools });
  assert.equal(prefix(requests[0]), prefix(requests.at(-1)));
  assert.ok((await browserText("#chat-log")).includes("acceptance: compaction"));
  record("compaction-keeps-model-prefix-stable");

  const screenshot = await page.screenshot();
	await page.goto(`http://127.0.0.1:${appPort}/?session=${sessionID}`);
	await browser.wait(`location.pathname==='/' && document.querySelector('#settings-page') && document.querySelector('.shell-settings')?.getAttribute('href')`, "Console settings control before Empty");
	await page.locator(".shell-settings").click();
	await browser.wait(`document.querySelector('#settings-page') && !document.querySelector('#settings-page').hidden`, "Settings open before Empty");
	assert.equal(await clickText(".settings-nav button", "Workspace"), true);
	await browser.wait(`!document.querySelector('#settings-page').hidden && [...document.querySelectorAll('.settings-content button')].some(item=>item.textContent.trim()==='Empty')`, "attachments Empty action");
	assert.equal(await clickText(".settings-content button", "Empty"), true);
	assert.equal(await clickText(".settings-content button", "Confirm empty"), true);
	for (let attempt = 0; attempt < 100; attempt++) {
		if ((await readdir(join(args.data, "attachments"))).length === 0) break;
		await sleep(50);
	}
	assert.deepEqual(await readdir(join(args.data, "attachments")), []);
	record("settings-confirmed-empty-attachments");
	await page.goto(`http://127.0.0.1:${appPort}/chat?session=${sessionID}`);
	await browser.wait(`document.querySelector('#chat-task')`, "chat restored after empty attachments");
	const finalState = await state();
	await json(`http://127.0.0.1:${appPort}/api/sessions/${sessionID}`, { method: "DELETE", headers: { "X-AgentB-Mutation-Token": finalState.mutation_token } });
	const exported = await waitEvent(sessionID, (event) => event.type === "chat.exported", "chat export");
	const exportedMarkdown = await readFile(exported.data.path, "utf8");
	assert.ok(exportedMarkdown.includes("## Transcript"));
	assert.ok(exportedMarkdown.includes("- tool `shell` · ok"));
	assert.ok(exportedMarkdown.includes("- attachment: `attachments/phone-note.txt`"));
	record("chat-close-markdown-export");
  const evidenceRun = join(args.evidence, `run-${new Date().toISOString().replaceAll(":", "-")}`);
  await mkdir(evidenceRun, { recursive: true });
  await writeFile(join(evidenceRun, "chat-final.png"), screenshot);
  await writeFile(join(evidenceRun, "result.json"), JSON.stringify({ scenarios, duration_ms: Date.now() - startedAt, session_id: sessionID }, null, 2));
  const evidenceLogs = join(evidenceRun, "jsonl");
  await mkdir(evidenceLogs, { recursive: true });
  for (const name of (await readdir(join(args.data, "logs"))).filter((item) => item.endsWith(".jsonl"))) {
    await writeFile(join(evidenceLogs, name), await readFile(join(args.data, "logs", name)));
  }
  await page.goto(`http://127.0.0.1:${appPort}/chat`);
  await browser.wait(`document.querySelector('.agent-tab')`, "agent tab after close");
  await page.locator(".agent-tab").first().click({ button: "right" });
  const closedRow = page.locator(`.agent-chat-row[data-session="${sessionID}"]`);
  await closedRow.waitFor({ state: "visible" });
  const finalMenuRows = await page.locator(".agent-chat-row").count();
  assert.match(await page.locator(".agent-chat-count").innerText(), new RegExp(`^${finalMenuRows} chats? · ${finalMenuRows - 1} open · 1 closed$`));
  const remove = closedRow.locator(".agent-chat-delete");
  await remove.click();
  await remove.filter({ hasText: "delete" }).waitFor({ state: "visible" });
  assert.match(await closedRow.locator(".agent-chat-summary").innerText(), /Delete permanently\? \d+ events · \d+ files · \d+ memory kept/);
  const dropMemory = closedRow.locator('.agent-chat-drop-memory input[type="checkbox"]');
  if (await dropMemory.count()) assert.equal(await dropMemory.isChecked(), false);
  await page.screenshot({ path: join(evidenceRun, "chat-delete-confirm.png") });
  await remove.click();
  for (let attempt = 0; attempt < 100; attempt++) {
    if (!(await state()).sessions[sessionID]) break;
    await sleep(50);
  }
  assert.equal((await state()).sessions[sessionID], undefined, "confirmed trash control must remove the session registry entry");
  record("agent-menu-inline-delete-keeps-memory-default");
}

record(realModel ? "real-model-script-complete" : "fake-model-script-complete");
process.stdout.write(`CHAT ACCEPTANCE PASS ${Date.now() - startedAt} ms\n`);

await edgeContext?.close();
terminateChildren();
await Promise.all(children.map((child) => waitForChildExit(child)));
await stopFake();
