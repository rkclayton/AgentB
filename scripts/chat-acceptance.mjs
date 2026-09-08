import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createServer } from "node:http";
import { mkdtemp, mkdir, readFile, readdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";

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
let app;
let model;
let modelPort;
let releaseQueue = null;
let releaseBusy = null;
const terminateChildren = () => {
  try { model?.closeAllConnections?.(); } catch {}
  for (const child of [...children].reverse()) { try { child.kill(); } catch {} }
};
process.on("exit", terminateChildren);

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
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
const fakeHandler = async (request, response) => {
  if (request.url === "/props") return void response.end(JSON.stringify({ server: "agentb-fake", n_ctx: 8192 }));
  if (request.url === "/v1/models") return void response.end(JSON.stringify({ data: [{ id: "agentb-fake" }] }));
  let raw = "";
  for await (const chunk of request) raw += chunk;
  const body = raw ? JSON.parse(raw) : {};
  response.agentbPromptTokens = Math.max(1, Math.ceil(JSON.stringify(body.messages || []).length / 4));
  if (request.url === "/tokenize") return void response.end(JSON.stringify({ tokens: Array.from({ length: Math.max(1, Math.ceil(String(body.content || body.prompt || "").length / 4)) }, (_, i) => i) }));
  if (request.url === "/apply-template") return void response.end(JSON.stringify({ prompt: JSON.stringify(body.messages || []) }));
  if (request.url !== "/v1/chat/completions") { response.statusCode = 404; return void response.end(); }
  const user = latestUser(body);
  if (user.includes("Summarize the work so far")) {
    response.setHeader("Content-Type", "application/json");
    return void response.end(JSON.stringify({ choices: [{ message: { content: "Earlier acceptance steps completed; keep the stable system and tool prefix." }, finish_reason: "stop" }], usage: { prompt_tokens: 300, completion_tokens: 18, prompt_tokens_details: { cached_tokens: 200 } } }));
  }
  if (user.includes("acceptance: stop")) return;
  if (user.includes("acceptance: queue leader")) {
    await new Promise((resolve) => { releaseQueue = resolve; response.on("close", resolve); });
    return stream(response, { content: "Queue leader completed." });
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

class CDP {
  constructor(url) {
    this.id = 0;
    this.pending = new Map();
    this.ws = new WebSocket(url);
    this.ready = new Promise((resolve, reject) => { this.ws.onopen = resolve; this.ws.onerror = reject; });
    this.ws.onmessage = ({ data }) => {
      const message = JSON.parse(data);
      if (!message.id) return;
      const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id);
      if (message.error) pending.reject(new Error(JSON.stringify(message.error)));
      else pending.resolve(message.result);
    };
  }
  async call(method, params = {}) {
    await this.ready;
    const id = ++this.id;
    const result = new Promise((resolve, reject) => this.pending.set(id, { resolve, reject }));
    this.ws.send(JSON.stringify({ id, method, params }));
    return result;
  }
  async evaluate(expression) {
    const result = await this.call("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
    if (result.exceptionDetails) throw new Error(`${result.exceptionDetails.text || "browser evaluation failed"}: ${result.exceptionDetails.exception?.description || expression}`);
    return result.result.value;
  }
  async wait(expression, label, timeout = 12000) {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      if (await this.evaluate(`Boolean(${expression})`)) return;
      await sleep(50);
    }
    throw new Error(`screen timeout: ${label}`);
  }
}

const browserText = async (selector) => browser.evaluate(`document.querySelector(${JSON.stringify(selector)})?.innerText || ""`);
const setTask = async (text) => browser.evaluate(`(() => { const input=document.querySelector('#chat-task'); input.value=${JSON.stringify(text)}; input.dispatchEvent(new Event('input',{bubbles:true})); document.querySelector('#chat-send').click(); return true; })()`);
const clickText = async (selector, text) => browser.evaluate(`(() => { const button=[...document.querySelectorAll(${JSON.stringify(selector)})].find(node => node.textContent.trim()===${JSON.stringify(text)}); if(!button)return false; button.click(); return true; })()`);
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
const browserPort = await freePort();
const gitPath = spawnSync("where.exe", ["git.exe"], { encoding: "utf8" }).stdout.split(/\r?\n/).find(Boolean);
assert.ok(gitPath, "Git is required for the Run as you acceptance scenario");
const bound = join(args.workspace, "..", "acceptance-bound");
await mkdir(bound, { recursive: true });
spawnSync("git.exe", ["init", "--quiet", bound], { stdio: "inherit" });
const attachment = join(args.workspace, "acceptance-attachment.txt");
await writeFile(attachment, "attachment acceptance bytes\n");
if (!realModel) await startFake();
const profileURL = realModel ? args["real-model-url"] : `http://127.0.0.1:${modelPort}`;
const profileName = realModel ? args["real-model-name"] : "agentb-fake";
const toolset = ["read_file", "list_dir", "write_file", "edit_file", "search_text", "shell", "remember", "recall", "fetch_url", "find_files", "run_script", "call_service"];
const config = {
  config_version: 6, listen: `127.0.0.1:${appPort}`, workspace: args.workspace, log_dir: join(args.data, "logs"),
  servers: [{ id: "acceptance", label: "Acceptance", base_url: profileURL, model: profileName, credential: "", request_timeout_s: 15, probe_mode: "off",
    sampling: { thinking: { temperature: .6, top_p: .95, top_k: 20, min_p: 0, presence_penalty: 0, repeat_penalty: 1 }, nonthinking: { temperature: .7, top_p: .8, top_k: 20, min_p: 0, presence_penalty: 0, repeat_penalty: 1 } },
    reasoning: { control: "auto", enabled: false, effort: "medium", valid_efforts: [], preserve: false }, context: { n_ctx: 8192, reserve_output: 1024 }, system_prompt_override: "",
    capabilities: { server: "agentb-fake", props: true, n_ctx: 8192, tokenize: true, apply_template: false, apply_template_tools: false, streaming: true, tool_calls: true, grammar_constrained: false, cached_tokens: true, timings: false, prompt_progress: false, document_input: false, image_input: false, reasoning_control: "", valid_efforts: [], overflow_behavior: "error", probed_at: new Date().toISOString(), findings: ["acceptance fake"] } }],
  services: {}, agents: [{ name: "Acceptance", b: "acceptance", toolset }], chat: { auto_rename: false },
  run: { max_turns: 12, cycle_window: 8, max_consecutive_tool_errors: 3, max_concurrent: 2, queue_depth: 0 }, approval: { mode: "boundary-only" },
  deliver: { mode: "chips", exchange_folder: join(args.workspace, "exchange") }, context: { soft_pct: .75, summary_pct: .85, accounting: "auto" }, memory: { enabled: false, dir: join(args.data, "memory"), max_tokens: 1500 },
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
const edge = process.env["ProgramFiles(x86)"] ? join(process.env["ProgramFiles(x86)"], "Microsoft", "Edge", "Application", "msedge.exe") : "msedge.exe";
const browserData = await mkdtemp(join(tmpdir(), "agentb-edge-"));
const edgeProcess = spawn(edge, ["--headless=new", `--remote-debugging-port=${browserPort}`, `--user-data-dir=${browserData}`, "--no-first-run", "--disable-extensions", `--app=http://127.0.0.1:${appPort}/chat`], { windowsHide: true, stdio: "ignore" });
children.push(edgeProcess);
let target;
for (let attempt = 0; attempt < 200; attempt++) {
  try { target = (await json(`http://127.0.0.1:${browserPort}/json/list`)).find((item) => item.type === "page"); if (target) break; } catch {}
  await sleep(50);
}
assert.ok(target?.webSocketDebuggerUrl, "Edge DevTools target did not appear");
browser = new CDP(target.webSocketDebuggerUrl);
await browser.call("Runtime.enable");
await browser.call("Page.enable");
await browser.wait(`document.querySelector('#chat-task')`, "Chat opened");
await browser.wait(`document.querySelector('.agent-tab')`, "Agent tab rendered");
record("open-chat");

if (realModel) {
  await setTask("Reply with the exact words REAL MODEL ACCEPTANCE OK.");
  await browser.wait(`document.querySelector('#chat-log')?.innerText.includes('REAL MODEL ACCEPTANCE OK')`, "real model answer", 120000);
  record("real-model-answer");
} else {
  let selectedWorkspace = false;
  for (let attempt = 0; attempt < 20 && !selectedWorkspace; attempt++) {
    await browser.evaluate(`(() => { const tab=document.querySelector('.agent-tab'); if(!tab)return false; tab.dispatchEvent(new MouseEvent('contextmenu',{bubbles:true})); const button=[...document.querySelectorAll('button.shell-new-choice')].find(item=>item.textContent.trim()==='New chat…'&&!item.disabled); if(!button)return false; button.click(); return true; })()`);
    for (let poll = 0; poll < 10 && !selectedWorkspace; poll++) {
      await sleep(50);
      selectedWorkspace = await browser.evaluate(`(() => { const button=[...document.querySelectorAll('button.shell-new-choice')].find(item=>item.textContent.startsWith('Default ·')&&!item.disabled); if(!button)return false; button.click(); return true; })()`);
    }
  }
  assert.equal(selectedWorkspace, true, "new-chat workspace choice did not remain mounted");
  record("agent-tab-context-menu");
  let snapshot;
  await browser.wait(`new URLSearchParams(location.search).get('session')?.startsWith('s')`, "new session selected");
  snapshot = await state();
  const sessionID = await browser.evaluate(`new URLSearchParams(location.search).get('session')`);
  const session = snapshot.sessions[sessionID];
  assert.equal(session?.id, sessionID, "selected new chat must exist in the server snapshot");
  record("new-chat");

  const geometry = await browser.evaluate(`(() => { const textarea=document.querySelector('#chat-task').getBoundingClientRect(); const row=document.querySelector('.chat-composer-row').getBoundingClientRect(); const expand=document.querySelector('#chat-expand').getBoundingClientRect(); const robot=document.querySelector('.agent-tab-wrap[data-agent="agent_b"] .agent-tab-robot').getBoundingClientRect(); return {textarea:textarea.width,row:row.width,expandTop:expand.top-textarea.top,expandRight:textarea.right-expand.right,robot:robot.width}; })()`);
  assert.ok(geometry.textarea >= geometry.row - 50, JSON.stringify(geometry));
  assert.ok(geometry.expandTop >= 0 && geometry.expandTop <= 8 && geometry.expandRight >= 0 && geometry.expandRight <= 8, JSON.stringify(geometry));
  assert.ok(geometry.robot > 0, JSON.stringify(geometry));
  record("composer-flex-width-expand-robot");

  await setTask(`Please inspect acceptance directory "${bound}" and report.`);
  await browser.wait(`document.querySelector('.workspace-bind-card')?.innerText.includes('Bind this chat to')`, "bind offer");
  await waitEvent(sessionID, (event) => event.type === "workspace.bind_required", "workspace.bind_required");
  assert.equal(await clickText(".workspace-bind-card button", "Yes"), true);
  await waitEvent(sessionID, (event) => event.type === "workspace.bound", "workspace.bound");
  await waitEvent(sessionID, (event) => event.type === "approval.required", "approval.required");
  await browser.wait(`[...document.querySelectorAll('.approval-card')].some(item=>item.innerText.toLowerCase().includes('run as you'))`, "Run as you card");
  assert.equal(await clickText(".approval-card button", "Yes, for this chat"), true);
  await browser.wait(`document.querySelector('#chat-log')?.innerText.includes('Acceptance answer rendered after the approved shell call.')`, "answer rendered");
  let events = await sessionEvents(sessionID);
  assert.ok(events.some((event) => event.type === "shell.grant" && event.data.scope === "session"));
  assert.ok(events.some((event) => event.type === "tool.result" && event.data.name === "shell" && event.data.ok === true));
  const gutter = await browser.evaluate(`getComputedStyle(document.querySelector('.chat-entry')).gridTemplateColumns.split(' ')[0]`);
  assert.match(gutter, /^90px$/);
  record("bind-run-as-you-tool-answer-gutter");

  await setTask("acceptance: stop");
  await browser.wait(`!document.querySelector('#chat-stop').disabled`, "stop enabled");
  const stopStart = Date.now();
  await browser.evaluate(`(() => { document.querySelector('#chat-stop').click(); return true; })()`);
  await browser.wait(`document.querySelector('#chat-stop').disabled`, "stop completed", 1000);
  assert.ok(Date.now() - stopStart < 1000, `Stop took ${Date.now() - stopStart} ms`);
  await waitEvent(sessionID, (event) => event.type === "run.stopped" && ["safe", "emergency", "user_stop"].includes(event.data.reason), "stopped run");
  record("stop-under-one-second");

  await setTask("acceptance: queue leader");
  await browser.wait(`!document.querySelector('#chat-stop').disabled`, "queue leader running");
  await setTask("acceptance: queued follower");
  await browser.wait(`document.querySelector('#chat-status-strip')?.innerText.includes('queued (1)')`, "queued count");
  await waitEvent(sessionID, (event) => event.type === "message.queued" && event.data.position === 1, "message.queued");
  releaseQueue?.();
  await browser.wait(`document.querySelector('#chat-log')?.innerText.includes('Queued follower completed.')`, "queued follower answer");
  events = await sessionEvents(sessionID);
  const queueUsers = events.filter((event) => event.type === "message.appended" && event.data.message?.role === "user").map((event) => event.data.message.content);
  assert.ok(queueUsers.indexOf("acceptance: queue leader") < queueUsers.indexOf("acceptance: queued follower"));
  record("active-run-queue-fifo");

  const root = await browser.call("DOM.getDocument");
  const picker = await browser.call("DOM.querySelector", { nodeId: root.root.nodeId, selector: "#chat-file-picker" });
  await browser.call("DOM.setFileInputFiles", { nodeId: picker.nodeId, files: [attachment] });
  await browser.evaluate(`(() => { document.querySelector('#chat-file-picker').dispatchEvent(new Event('change',{bubbles:true})); return true; })()`);
  await browser.wait(`document.querySelector('.chat-pending-file')`, "pending attachment");
  await setTask("acceptance: attachment");
  await browser.wait(`document.querySelector('#chat-log')?.innerText.includes('Attachment received and rendered.')`, "attachment answer");
  await waitEvent(sessionID, (event) => event.type === "message.appended" && event.data.message?.attachments?.length === 1, "attachment JSONL");
  record("attachment-screen-jsonl");

  const beforeReload = (await browserText("#chat-log")).slice(0, 120);
  await browser.call("Page.reload", { ignoreCache: true });
  await browser.wait(`document.querySelector('#chat-log')?.innerText.includes('Attachment received and rendered.')`, "chat reopen");
  assert.equal((await browserText("#chat-log")).slice(0, 120), beforeReload);
  record("chat-reopen-preserves-screen-and-jsonl");

  await stopFake();
  await setTask("acceptance: unreachable");
  await browser.wait(`document.querySelector('#chat-status-strip')?.innerText.includes('model unreachable')`, "unreachable strip");
  await waitEvent(sessionID, (event) => event.type === "model.unreachable", "model.unreachable");
  await setTask("acceptance: recovered");
  await browser.wait(`document.querySelector('#chat-status-strip')?.innerText.includes('queued (1)')`, "recovery queued");
  await startFake(modelPort);
  await browser.evaluate(`(() => { document.querySelector('#chat-retry-model').click(); return true; })()`);
  await browser.wait(`document.querySelector('#chat-log')?.innerText.includes('Recovered after Retry.')`, "Retry recovery", 20000);
  await waitEvent(sessionID, (event) => event.type === "model.reachable", "model.reachable");
  record("model-unreachable-retry-release");

  await setTask("acceptance: busy");
  await browser.wait(`document.querySelector('#chat-status-strip')?.innerText.includes('model busy')`, "busy strip", 6000);
  events = await sessionEvents(sessionID);
  const busyEvent = events.findLast((event) => event.type === "model.busy");
  assert.ok(busyEvent);
  assert.equal(events.slice(events.indexOf(busyEvent)).some((event) => event.type === "run.stopped"), false);
  releaseBusy?.();
  await browser.wait(`document.querySelector('#chat-log')?.innerText.includes('Busy model resumed.')`, "busy resumed");
  await waitEvent(sessionID, (event) => event.type === "run.stopped" && event.seq > busyEvent.seq, "busy run stopped");
  record("model-busy-waits-without-stop");

  for (let index = 0; index < 12; index++) {
    events = await sessionEvents(sessionID);
    const beforeSequence = events.at(-1)?.seq || 0;
    await setTask(`acceptance: compaction ${index} ${"payload ".repeat(300)}`);
    await waitEvent(sessionID, (event) => event.type === "run.stopped" && event.seq > beforeSequence, `compaction run ${index}`, 20000);
  }
  const compaction = await waitEvent(sessionID, (event) => event.type === "compaction", "compaction", 20000);
  assert.ok(compaction.data.before > compaction.data.after);
  events = await sessionEvents(sessionID);
  const requests = events.filter((event) => event.type === "model.request" && event.body);
  const prefix = (event) => JSON.stringify({ system: event.body.messages?.[0], tools: event.body.tools });
  assert.equal(prefix(requests[0]), prefix(requests.at(-1)));
  assert.ok((await browserText("#chat-log")).includes("acceptance: compaction"));
  record("compaction-keeps-model-prefix-stable");

  const screenshot = await browser.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
  const evidenceRun = join(args.evidence, `run-${new Date().toISOString().replaceAll(":", "-")}`);
  await mkdir(evidenceRun, { recursive: true });
  await writeFile(join(evidenceRun, "chat-final.png"), Buffer.from(screenshot.data, "base64"));
  await writeFile(join(evidenceRun, "result.json"), JSON.stringify({ scenarios, duration_ms: Date.now() - startedAt, session_id: sessionID }, null, 2));
  const evidenceLogs = join(evidenceRun, "jsonl");
  await mkdir(evidenceLogs, { recursive: true });
  for (const name of (await readdir(join(args.data, "logs"))).filter((item) => item.endsWith(".jsonl"))) {
    await writeFile(join(evidenceLogs, name), await readFile(join(args.data, "logs", name)));
  }
}

record(realModel ? "real-model-script-complete" : "fake-model-script-complete");
process.stdout.write(`CHAT ACCEPTANCE PASS ${Date.now() - startedAt} ms\n`);

try { browser?.ws?.close(); } catch {}
terminateChildren();
await stopFake();
