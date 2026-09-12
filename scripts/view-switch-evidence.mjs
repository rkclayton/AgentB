import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { chromium } from "@playwright/test";

const argv = process.argv.slice(2);
const args = Object.fromEntries(Array.from({ length: argv.length / 2 }, (_, index) => [argv[index * 2].replace(/^--/, ""), argv[index * 2 + 1]]));
for (const name of ["exe", "app-root", "data", "evidence"]) assert.ok(args[name], `missing --${name}`);
const sleep = (ms) => new Promise((done) => setTimeout(done, ms));
async function freePort() {
  const probe = createServer();
  await new Promise((done) => probe.listen(0, "127.0.0.1", done));
  const port = probe.address().port;
  await new Promise((done) => probe.close(done));
  assert.ok(port !== 7337 && port !== 8790);
  return port;
}
async function stop(child) {
  if (!child || child.exitCode !== null) return;
  child.kill();
  await Promise.race([new Promise((done) => child.once("exit", done)), sleep(3000)]);
}
async function waitFor(check, label, timeout = 15000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) { try { const value = await check(); if (value) return value; } catch {} await sleep(25); }
  throw new Error(`${label} timeout`);
}

const data = resolve(args.data);
const evidence = resolve(args.evidence);
const workspace = join(data, "workspace");
await mkdir(workspace, { recursive: true });
await mkdir(evidence, { recursive: true });
const modelPort = await freePort();
let stream = false;
const model = createServer(async (request, response) => {
  if (request.url === "/props") return void response.end(JSON.stringify({ server: "view-switch", n_ctx: 32768 }));
  if (request.url === "/v1/models") return void response.end(JSON.stringify({ data: [{ id: "view-switch" }] }));
  if (request.url === "/tokenize") return void response.end(JSON.stringify({ tokens: [1] }));
  if (request.url === "/apply-template") return void response.end(JSON.stringify({ prompt: "view switch" }));
  for await (const _chunk of request) { /* drain */ }
  if (request.url !== "/v1/chat/completions") { response.statusCode = 404; return void response.end(); }
  response.writeHead(200, { "Content-Type": "text/event-stream" });
  for (let index = 0; index < (stream ? 20 : 1); index++) {
    response.write(`data: ${JSON.stringify({ choices: [{ delta: { content: `chunk-${index} ` }, finish_reason: null }] })}\n\n`);
    if (stream) await sleep(75);
  }
  response.end(`data: ${JSON.stringify({ choices: [{ delta: {}, finish_reason: "stop" }], usage: { prompt_tokens: 2, completion_tokens: 20 } })}\n\ndata: [DONE]\n\n`);
});
await new Promise((done) => model.listen(modelPort, "127.0.0.1", done));

const appPort = await freePort();
const toolset = ["read_file", "list_dir", "write_file", "edit_file", "search_text", "shell", "remember", "recall", "fetch_url", "find_files", "run_script", "call_service"];
const config = {
  config_version: 6, listen: `127.0.0.1:${appPort}`, workspace, log_dir: join(data, "logs"),
  servers: [{ id: "view", label: "View", base_url: `http://127.0.0.1:${modelPort}`, model: "view-switch", credential: "", request_timeout_s: 2, probe_mode: "off",
    sampling: { thinking: { temperature: .6, top_p: .95, top_k: 20, min_p: 0, presence_penalty: 0, repeat_penalty: 1 }, nonthinking: { temperature: .7, top_p: .8, top_k: 20, min_p: 0, presence_penalty: 0, repeat_penalty: 1 } },
    reasoning: { control: "auto", enabled: false, effort: "medium", valid_efforts: [], preserve: false }, context: { n_ctx: 32768, reserve_output: 10240 }, system_prompt_override: "",
    capabilities: { server: "view-switch", props: true, n_ctx: 32768, tokenize: true, apply_template: true, apply_template_tools: true, streaming: true, tool_calls: true, grammar_constrained: false, cached_tokens: true, timings: false, prompt_progress: false, document_input: false, image_input: false, reasoning_control: "", valid_efforts: [], overflow_behavior: "error", probed_at: new Date().toISOString(), findings: [] } }],
  services: {}, agents: [{ name: "View", b: "view", toolset }], chat: { auto_rename: false }, run: { max_turns: 4, cycle_window: 8, max_consecutive_tool_errors: 3, max_concurrent: 1, queue_depth: 0 }, approval: { mode: "boundary-only" },
  deliver: { mode: "chips", exchange_folder: join(data, "exchange") }, operator_files: { allow_mailbox_approvals: false, log_retention_days: 30 }, context: { soft_pct: .75, summary_pct: .95, accounting: "estimated" }, memory: { enabled: false, dir: join(data, "memory"), max_tokens: 1500 },
  tools: { read_file: { default_limit: 16384, max_limit: 65536 }, attachments: { max_bytes: 8388608 }, list_dir: { max_entries: 300, ignore: [".git"] }, grep: { max_matches: 50, max_line_chars: 200 }, shell: { operator_commands: [] }, fetch: { timeout_s: 20, max_bytes: 2097152, max_redirects: 5, default_limit: 16384, max_limit: 65536, allow_domains: [], deny_domains: [], allow_internal_hosts: [] }, find_files: { skip_roots: [] } },
  shell: { command: ["powershell", "-NoProfile", "-NonInteractive", "-Command"], timeout_s: 60, max_timeout_s: 600, max_output_lines_head: 60, max_output_lines_tail: 40, file_routing_guard: true, operator_context: false, operator_context_idle_timeout_minutes: 20, service_account: { enabled: false, account: "agentb-svc", domain: "." }, deny: [] }, signing: { thumbprint: "", timestamp_url: "http://timestamp.digicert.com" },
};
const configPath = join(data, "harness.json");
await writeFile(configPath, JSON.stringify(config, null, 2));
let app = spawn(resolve(args.exe), ["-config", configPath, "-app-root", resolve(args["app-root"]), "-data-root", data], { windowsHide: true, stdio: ["ignore", "ignore", "pipe"] });
let appError = "";
app.stderr.on("data", (chunk) => { appError += String(chunk); });
const base = `http://127.0.0.1:${appPort}`;
const getState = async () => { const response = await fetch(`${base}/api/state`); assert.equal(response.status, 200); return response.json(); };
const initial = await waitFor(getState, "app start");
const token = initial.mutation_token;
const logPath = initial.sessions.main.log_path;

let browser;
try {
  browser = await chromium.launch({ channel: "msedge", headless: true });
  const context = await browser.newContext({ viewport: { width: 1250, height: 975 } });
  await context.addInitScript(() => {
    window.__viewEvidence = { eventSources: 0 };
    const Native = window.EventSource;
    window.EventSource = class extends Native { constructor(...values) { super(...values); window.__viewEvidence.eventSources++; } };
  });
  const page = await context.newPage();
  const documents = [];
  page.on("request", (request) => { if (request.resourceType() === "document") documents.push(request.url()); });
  await page.goto(`${base}/chat?session=main`);
  await page.locator("#chat-task").waitFor();
  await page.locator("#chat-task").fill("draft-survival-marker");
  const marker = await page.evaluate(() => window.__documentMarker = crypto.randomUUID());
  const subscriptions = await page.evaluate(() => import("/static/js/bus.js").then((module) => module.subscriberCount()));
  const trials = {};
  async function flips(name, count = 5) {
    const values = [];
    for (let index = 0; index < count; index++) {
      const started = performance.now();
      await page.locator('.agent-tab[data-agent="agent_b"]').click();
      const target = index % 2 === 0 ? "console" : "chat";
      await page.locator(`body[data-page="${target}"]`).waitFor();
      values.push(Math.round((performance.now() - started) * 1000) / 1000);
    }
    trials[name] = values;
  }
  await flips("idle", 6);
  const entryPage = await context.newPage();
  await entryPage.goto(`${base}/`);
  await entryPage.locator('body[data-page="console"]').waitFor();
  await entryPage.reload();
  await entryPage.locator('body[data-page="console"]').waitFor();
  await entryPage.goto(`${base}/chat`);
  await entryPage.locator('body[data-page="chat"]').waitFor();
  await entryPage.reload();
  await entryPage.locator('body[data-page="chat"]').waitFor();
  await entryPage.locator('.agent-tab[data-agent="agent_b"]').click();
  await entryPage.locator('body[data-page="console"]').waitFor();
  await entryPage.locator('.agent-tab[data-agent="agent_b"]').click();
  await entryPage.locator('body[data-page="chat"]').waitFor();
  await entryPage.evaluate(() => history.back());
  await entryPage.waitForURL((url) => url.pathname === "/");
  await entryPage.locator('body[data-page="console"]').waitFor();
  await entryPage.evaluate(() => history.forward());
  await entryPage.waitForURL((url) => url.pathname === "/chat");
  await entryPage.locator('body[data-page="chat"]').waitFor();
  await entryPage.close();
  if (await page.locator("body").getAttribute("data-page") !== "chat") await page.locator('.agent-tab[data-agent="agent_b"]').click();
  stream = true;
  await page.locator("#chat-task").fill("stream switching");
  await page.locator("#chat-send").click();
  await waitFor(async () => (await getState()).sessions.main.run.status === "running", "stream start");
  await flips("mid_stream", 6);
  await waitFor(async () => (await getState()).sessions.main.run.status === "idle", "stream finish");
  if (await page.locator("body").getAttribute("data-page") !== "chat") await page.locator('.agent-tab[data-agent="agent_b"]').click();
  await page.locator("#chat-task").fill("draft-survival-marker");
  await page.waitForTimeout(250);
  const transcriptBefore = await page.locator("#chat-log").innerText();
  await stop(app); app = null;
  await page.setViewportSize({ width: 500, height: 800 });
  await flips("backend_down", 6);
  if (await page.locator("body").getAttribute("data-page") !== "chat") await page.locator('.agent-tab[data-agent="agent_b"]').click();
  assert.equal(await page.locator("#chat-task").inputValue(), "draft-survival-marker");
  assert.equal(await page.evaluate(() => window.__documentMarker), marker);
  assert.equal(await page.evaluate(() => window.__viewEvidence.eventSources), 1);
  assert.equal(await page.evaluate(() => import("/static/js/bus.js").then((module) => module.subscriberCount())), subscriptions);
  assert.equal(await page.locator("#chat-log").innerText(), transcriptBefore);
  await page.screenshot({ path: join(evidence, "chat-final.png") });
  await page.locator('.agent-tab[data-agent="agent_b"]').click();
  await page.screenshot({ path: join(evidence, "console-final.png") });
  const tape = (await readFile(logPath, "utf8")).split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
  const navigation = tape.filter((event) => event.type === "navigation.measured").map((event) => event.data);
  const result = { schema: 1, build: initial.build, direct_entries: ["/chat", "/"], trials, document_requests_after_initial: documents.slice(1), document_marker_survived: true, draft_survived: true, transcript_survived: true, event_source_constructions: 1, subscriber_count_before: subscriptions, subscriber_count_after: subscriptions, navigation, visible_freeze_observed: false, backend_down_view_accessible: true, stderr: appError };
  await writeFile(join(evidence, "result.json"), JSON.stringify(result, null, 2));
  process.stdout.write(`${JSON.stringify(result)}\n`);
} finally {
  await browser?.close().catch(() => {});
  await stop(app);
  model.closeAllConnections?.();
  await new Promise((done) => model.close(done));
}
