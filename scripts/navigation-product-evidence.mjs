import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { chromium } from "playwright";

const args = Object.fromEntries(Array.from({ length: Math.floor(process.argv.slice(2).length / 2) }, (_, index) => {
  const offset = index * 2 + 2;
  return [process.argv[offset].replace(/^--/, ""), process.argv[offset + 1]];
}));
for (const name of ["exe", "app-root", "data", "evidence"]) assert.ok(args[name], `missing --${name}`);
const largeTurns = Number(args["large-turns"] || 180);
const sleep = (ms) => new Promise((resolveSleep) => setTimeout(resolveSleep, ms));

async function freePort() {
  const probe = createServer();
  await new Promise((done) => probe.listen(0, "127.0.0.1", done));
  const port = probe.address().port;
  await new Promise((done) => probe.close(done));
  return port;
}

const model = createServer(async (request, response) => {
  if (request.url === "/props") return void response.end(JSON.stringify({ server: "navigation-evidence", n_ctx: 131072 }));
  if (request.url === "/v1/models") return void response.end(JSON.stringify({ data: [{ id: "navigation-evidence" }] }));
  if (request.url === "/tokenize") return void response.end(JSON.stringify({ tokens: [1] }));
  if (request.url === "/apply-template") return void response.end(JSON.stringify({ prompt: "navigation evidence" }));
  for await (const _chunk of request) { /* drain */ }
  if (request.url !== "/v1/chat/completions") { response.statusCode = 404; return void response.end(); }
  response.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
  response.end(`data: ${JSON.stringify({ choices: [{ delta: { content: "recorded" }, finish_reason: "stop" }], usage: { prompt_tokens: 10, completion_tokens: 1, prompt_tokens_details: { cached_tokens: 0 } } })}\n\ndata: [DONE]\n\n`);
});

const children = [];
let browser;
const stopChildren = () => {
  try { browser?.close(); } catch {}
  try { model.closeAllConnections?.(); model.close(); } catch {}
  for (const child of children.reverse()) try { child.kill(); } catch {}
};
process.on("exit", stopChildren);

const modelPort = await freePort();
await new Promise((done) => model.listen(modelPort, "127.0.0.1", done));
const appPort = await freePort();
const dataRoot = resolve(args.data);
const workspace = join(dataRoot, "workspace");
await mkdir(workspace, { recursive: true });
const toolset = ["read_file", "list_dir", "write_file", "edit_file", "search_text", "shell", "remember", "recall", "fetch_url", "find_files", "run_script", "call_service"];
const config = {
  config_version: 6, listen: `127.0.0.1:${appPort}`, workspace, log_dir: join(dataRoot, "logs"),
  servers: [{ id: "navigation", label: "Navigation", base_url: `http://127.0.0.1:${modelPort}`, model: "navigation-evidence", credential: "", request_timeout_s: 3, probe_mode: "off",
    sampling: { thinking: { temperature: .6, top_p: .95, top_k: 20, min_p: 0, presence_penalty: 0, repeat_penalty: 1 }, nonthinking: { temperature: .7, top_p: .8, top_k: 20, min_p: 0, presence_penalty: 0, repeat_penalty: 1 } },
    reasoning: { control: "auto", enabled: false, effort: "medium", valid_efforts: [], preserve: false }, context: { n_ctx: 131072, reserve_output: 10240 }, system_prompt_override: "",
    capabilities: { server: "navigation-evidence", props: true, n_ctx: 131072, tokenize: true, apply_template: true, apply_template_tools: true, streaming: true, tool_calls: true, grammar_constrained: false, cached_tokens: true, timings: false, prompt_progress: false, document_input: false, image_input: false, reasoning_control: "", valid_efforts: [], overflow_behavior: "error", probed_at: new Date().toISOString(), findings: ["navigation evidence fixture"] } }],
  services: {}, agents: [{ name: "Navigation", b: "navigation", toolset }], chat: { auto_rename: false },
  run: { max_turns: 4, cycle_window: 8, max_consecutive_tool_errors: 3, max_concurrent: 1, queue_depth: 0 }, approval: { mode: "boundary-only" },
  deliver: { mode: "chips", exchange_folder: join(dataRoot, "exchange") }, operator_files: { allow_mailbox_approvals: false, log_retention_days: 30 },
  context: { soft_pct: .75, summary_pct: .95, accounting: "estimated" }, memory: { enabled: false, dir: join(dataRoot, "memory"), max_tokens: 1500 },
  tools: { read_file: { default_limit: 16384, max_limit: 65536 }, attachments: { max_bytes: 8388608 }, list_dir: { max_entries: 300, ignore: [".git"] }, grep: { max_matches: 50, max_line_chars: 200 }, shell: { operator_commands: [] }, fetch: { timeout_s: 20, max_bytes: 2097152, max_redirects: 5, default_limit: 16384, max_limit: 65536, allow_domains: [], deny_domains: [], allow_internal_hosts: [] }, find_files: { skip_roots: [] } },
  shell: { command: ["powershell", "-NoProfile", "-NonInteractive", "-Command"], timeout_s: 60, max_timeout_s: 600, max_output_lines_head: 60, max_output_lines_tail: 40, file_routing_guard: true, operator_context: false, operator_context_idle_timeout_minutes: 20, service_account: { enabled: false, account: "agentb-svc", domain: "." }, deny: [] },
  signing: { thumbprint: "", timestamp_url: "http://timestamp.digicert.com" },
};
const configPath = join(dataRoot, "harness.json");
await writeFile(configPath, JSON.stringify(config, null, 2));

const app = spawn(resolve(args.exe), ["-config", configPath, "-app-root", resolve(args["app-root"]), "-data-root", dataRoot], { windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
children.push(app);
let stderr = "";
app.stderr.on("data", (chunk) => { stderr += String(chunk); });

const base = `http://127.0.0.1:${appPort}`;
async function waitFor(check, label, timeout = 15000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    try { const value = await check(); if (value) return value; } catch {}
    await sleep(25);
  }
  throw new Error(`${label} timeout\n${stderr}`);
}
async function state() {
  const response = await fetch(`${base}/api/state`, { cache: "no-store" });
  assert.equal(response.status, 200);
  return response.json();
}
const initial = await waitFor(state, "candidate startup");
assert.equal(initial.build.tag, "v0.26.0");
assert.ok(initial.sessions.main, "main chat missing");
const token = initial.mutation_token;
async function addTurn(index) {
  const before = (await state()).sessions.main.chat.length;
  const response = await fetch(`${base}/api/message`, { method: "POST", headers: { "Content-Type": "application/json", "X-AgentB-Mutation-Token": token }, body: JSON.stringify({ session_id: "main", text: `navigation evidence turn ${index}` }) });
  assert.equal(response.status, 202, await response.text());
  return waitFor(async () => {
    const value = await state();
    return value.sessions.main.run.status === "idle" && value.sessions.main.chat.length >= before + 2 ? value : null;
  }, `turn ${index}`, 10000);
}

await addTurn(1);
await addTurn(2);

browser = await chromium.launch({ channel: "msedge", headless: true });
async function exerciseNavigations(expectedMinimumEntries) {
  const context = await browser.newContext({ viewport: { width: 1250, height: 975 } });
  const page = await context.newPage();
  let expectedEvents = (await readNavigationEvents()).length;
  const waitForTapeEntry = async () => {
    expectedEvents++;
    await waitFor(async () => {
      const events = await readNavigationEvents();
      return events.length >= expectedEvents && Number(events[expectedEvents - 1]?.data?.transcript_entries) >= expectedMinimumEntries;
    }, `navigation tape event ${expectedEvents}`);
  };
  await page.goto(`${base}/chat?session=main`);
  await page.locator('.agent-tab[data-agent="agent_b"]').waitFor();
  await page.locator('.agent-tab[data-agent="agent_b"]').click();
  await page.waitForURL((url) => url.pathname === "/" && !url.hash);
  await waitForTapeEntry();
  await page.locator('.agent-tab[data-agent="agent_b"]').click();
  await page.waitForURL((url) => url.pathname === "/chat");
  await waitForTapeEntry();
  await page.locator(".shell-settings").click();
  await page.waitForURL((url) => url.pathname === "/" && url.hash.startsWith("#settings/"));
  await page.locator("#settings-page:not([hidden])").waitFor();
  await waitForTapeEntry();
  await page.locator(".shell-settings").click();
  await page.locator("#settings-page").waitFor({ state: "hidden" });
  await waitForTapeEntry();
  await context.close();
}

async function readNavigationEvents() {
  const current = await state();
  const logPath = current.sessions.main.log_path;
  const text = await readFile(logPath, "utf8");
  return text.split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line)).filter((event) => event.type === "navigation.measured");
}

await exerciseNavigations(4);
const smallEvents = (await readNavigationEvents()).slice();
for (let index = 3; index <= largeTurns + 2; index++) await addTurn(index);
const largeState = await state();
assert.ok(largeState.sessions.main.chat.length >= largeTurns * 2, `large transcript has only ${largeState.sessions.main.chat.length} entries`);
await exerciseNavigations(largeTurns * 2);
const allEvents = await readNavigationEvents();
const largeEvents = allEvents.slice(smallEvents.length);
assert.equal(smallEvents.length, 4);
assert.equal(largeEvents.length, 4);
assert.deepEqual(smallEvents.map((event) => event.data.navigation_kind), ["flip", "flip", "settings", "settings"]);
assert.deepEqual(largeEvents.map((event) => event.data.navigation_kind), ["flip", "flip", "settings", "settings"]);

const logPath = (await state()).sessions.main.log_path;
const output = {
  schema: 1,
  measured_at: new Date().toISOString(),
  build: initial.build,
  transcript: { small_entries: smallEvents[0].data.transcript_entries, large_entries: largeEvents[0].data.transcript_entries, turns_added: largeTurns },
  small: smallEvents.map((event) => event.data),
  large: largeEvents.map((event) => event.data),
  tape: { path: logPath, navigation_events: allEvents.length },
};
await mkdir(resolve(args.evidence), { recursive: true });
await writeFile(join(resolve(args.evidence), "raw.json"), JSON.stringify(output, null, 2));
process.stdout.write(`${JSON.stringify(output, null, 2)}\n`);
stopChildren();
