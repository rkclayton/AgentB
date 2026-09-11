import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { chromium } from "playwright";

const argv = process.argv.slice(2);
const args = Object.fromEntries(Array.from({ length: Math.floor(argv.length / 2) }, (_, index) => [argv[index * 2].replace(/^--/, ""), argv[index * 2 + 1]]));
for (const name of ["exe", "app-root", "data", "evidence", "expected-commit"]) assert.ok(args[name], `missing --${name}`);
const sleep = (ms) => new Promise((done) => setTimeout(done, ms));

async function freePort() {
  const probe = createServer();
  await new Promise((done) => probe.listen(0, "127.0.0.1", done));
  const port = probe.address().port;
  await new Promise((done) => probe.close(done));
  assert.notEqual(port, 7337);
  assert.notEqual(port, 8790);
  return port;
}

const modelPort = await freePort();
const failedPort = await freePort();
const appPort = await freePort();
const model = createServer(async (request, response) => {
  if (request.url === "/props") return void response.end(JSON.stringify({ server: "settings-evidence", n_ctx: 32768 }));
  if (request.url === "/v1/models") return void response.end(JSON.stringify({ data: [{ id: "settings-evidence" }] }));
  if (request.url === "/tokenize") return void response.end(JSON.stringify({ tokens: [1, 2] }));
  if (request.url === "/apply-template") return void response.end(JSON.stringify({ prompt: "probe_tool" }));
  let body = "";
  for await (const chunk of request) body += String(chunk);
  if (request.url !== "/v1/chat/completions") { response.statusCode = 404; return void response.end(); }
  const input = JSON.parse(body || "{}");
  if (input.stream) {
    response.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
    return void response.end(`data: ${JSON.stringify({ choices: [{ delta: { content: "OK" }, finish_reason: "stop" }], usage: { prompt_tokens: 4, completion_tokens: 1, prompt_tokens_details: { cached_tokens: 0 } } })}\n\ndata: [DONE]\n\n`);
  }
  response.end(JSON.stringify({ choices: [{ message: { role: "assistant", content: "OK" }, finish_reason: "stop" }], usage: { prompt_tokens: 4, completion_tokens: 1 } }));
});

const dataRoot = resolve(args.data);
const workspace = join(dataRoot, "workspace");
await mkdir(workspace, { recursive: true });
const toolset = ["read_file", "list_dir", "write_file", "edit_file", "search_text", "shell", "remember", "recall", "fetch_url", "find_files", "run_script", "call_service"];
const profile = {
  id: "seed", label: "Seed", base_url: `http://127.0.0.1:${modelPort}`, extract_url: "", model: "settings-evidence", credential: "", request_timeout_s: 3, probe_mode: "minimal",
  sampling: { thinking: { temperature: .6, top_p: .95, top_k: 20, min_p: 0, presence_penalty: 0, repeat_penalty: 1 }, nonthinking: { temperature: .7, top_p: .8, top_k: 20, min_p: 0, presence_penalty: 1.5, repeat_penalty: 1 } },
  reasoning: { control: "auto", enabled: false, effort: "medium", valid_efforts: [], preserve: false }, context: { n_ctx: 32768, reserve_output: 10240 }, system_prompt_override: "",
  capabilities: { server: "llama.cpp", props: true, n_ctx: 32768, tokenize: true, apply_template: true, apply_template_tools: true, streaming: true, tool_calls: true, grammar_constrained: false, cached_tokens: false, timings: false, prompt_progress: false, document_input: false, image_input: false, reasoning_control: "none", valid_efforts: [], overflow_behavior: "error", probed_at: new Date().toISOString(), findings: ["seed fixture"] },
};
const config = {
  config_version: 6, listen: `127.0.0.1:${appPort}`, workspace, log_dir: join(dataRoot, "logs"), servers: [profile], services: {}, agents: [{ name: "Settings evidence", b: "seed", toolset }], chat: { auto_rename: false },
  run: { max_turns: 4, cycle_window: 8, max_consecutive_tool_errors: 3, max_concurrent: 1, queue_depth: 0 }, approval: { mode: "boundary-only" }, deliver: { mode: "chips", exchange_folder: join(dataRoot, "exchange") }, operator_files: { allow_mailbox_approvals: false, log_retention_days: 30 },
  context: { soft_pct: .75, summary_pct: .95, accounting: "estimated" }, memory: { enabled: false, dir: join(dataRoot, "memory"), max_tokens: 1500 },
  tools: { read_file: { default_limit: 16384, max_limit: 65536 }, attachments: { max_bytes: 8388608 }, list_dir: { max_entries: 300, ignore: [".git"] }, grep: { max_matches: 50, max_line_chars: 200 }, shell: { operator_commands: [] }, fetch: { timeout_s: 20, max_bytes: 2097152, max_redirects: 5, default_limit: 16384, max_limit: 65536, allow_domains: [], deny_domains: [], allow_internal_hosts: [] }, find_files: { skip_roots: [] } },
  shell: { command: ["powershell", "-NoProfile", "-NonInteractive", "-Command"], timeout_s: 60, max_timeout_s: 600, max_output_lines_head: 60, max_output_lines_tail: 40, file_routing_guard: true, operator_context: false, operator_context_idle_timeout_minutes: 20, service_account: { enabled: false, account: "agentb-svc", domain: "." }, deny: [] }, signing: { thumbprint: "", timestamp_url: "http://timestamp.digicert.com" },
};
const configPath = join(dataRoot, "harness.json");
await writeFile(configPath, JSON.stringify(config, null, 2));

const children = [];
let browser;
let stderr = "";
try {
  await new Promise((done) => model.listen(modelPort, "127.0.0.1", done));
  const app = spawn(resolve(args.exe), ["-config", configPath, "-app-root", resolve(args["app-root"]), "-data-root", dataRoot], { windowsHide: true, stdio: ["ignore", "ignore", "pipe"] });
  children.push(app);
  app.stderr.on("data", (chunk) => { stderr += String(chunk); });
  const base = `http://127.0.0.1:${appPort}`;
  async function state() { const response = await fetch(`${base}/api/state`); assert.equal(response.status, 200); return response.json(); }
  async function configure(body) {
    const current = await state();
    const response = await fetch(`${base}/api/config`, { method: "POST", headers: { "Content-Type": "application/json", "X-AgentB-Mutation-Token": current.mutation_token }, body: JSON.stringify(body) });
    const text = await response.text();
    assert.equal(response.status, 200, text);
    return JSON.parse(text);
  }
  async function waitFor(check, label, timeout = 20000) {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) { try { const value = await check(); if (value) return value; } catch {} await sleep(50); }
    throw new Error(`${label} timeout: ${stderr}`);
  }
  const initial = await waitFor(state, "candidate startup");
  assert.equal(initial.build.commit, args["expected-commit"]);
  assert.equal(initial.build.dirty, false);
  browser = await chromium.launch({ channel: "msedge", headless: true });
  const page = await browser.newPage({ viewport: { width: 1250, height: 975 } });
  await page.goto(`${base}/`);
  await page.locator(".shell-settings").click();
  await page.locator('.profile-summary[data-id="seed"]').click();
  assert.equal(await page.locator(".profile-editor").count(), 1);
  await page.getByRole("button", { name: "Add connection" }).click();
  await page.locator('.profile-summary[data-id="server"]').waitFor();
  assert.equal(await page.locator(".profile-editor").count(), 1);
  assert.equal(await page.locator('.profile-editor[aria-label="server connection settings"]').count(), 1);
  const persistedAfterAdd = (await state()).config.servers.find((item) => item.id === "server");
  assert.ok(persistedAfterAdd, "Add connection did not persist the new profile");
  const baseInput = page.locator('[data-path="servers.server.base_url"]');
  await baseInput.fill(`http://127.0.0.1:${modelPort}`);
  await page.locator('[data-path="servers.server.model"]').fill("settings-evidence");
  await page.locator('[data-action="config-choice"][data-path="servers.server.probe_mode"][data-value="minimal"]').click();
  const rowState = page.locator('.profile-summary[data-id="server"] .profile-state');
  const unsaved = await rowState.textContent();
  assert.match(unsaved, /unsaved.*Test will save first/i);
  const testButton = page.locator('.profile-row:has(.profile-summary[data-id="server"]) [data-action="probe"]');
  assert.equal(await testButton.isEnabled(), true);
  await testButton.click();
  await waitFor(async () => /Test passed/.test(await rowState.textContent()), "successful visible Test result");
  const successState = await rowState.textContent();
  const contextValue = await waitFor(async () => {
    const value = await page.locator('[data-path="servers.server.context.n_ctx"]').inputValue();
    return value === "32768" ? value : null;
  }, "probed context size in editor");
  assert.equal(contextValue, "32768");
  assert.equal(await page.getByText("context length unknown", { exact: true }).count(), 0);
  const savedByTest = (await state()).config.servers.find((item) => item.id === "server");
  assert.equal(savedByTest.base_url, `http://127.0.0.1:${modelPort}`);
  assert.equal(savedByTest.context.n_ctx, 32768);

  await baseInput.fill(`http://127.0.0.1:${failedPort}`);
  await testButton.click();
  await waitFor(async () => /Test failed/.test(await rowState.textContent()), "failed visible Test result", 30000);
  const failureState = await rowState.textContent();
  assert.match(failureState, /Test failed/i);

  await baseInput.fill(`http://127.0.0.1:${modelPort}`);
  await testButton.click();
  await waitFor(async () => /Test passed/.test(await rowState.textContent()), "restored successful Test result");
  await page.locator('[data-path="servers.server.label"]').fill("Evidence connection");
  await page.locator('[data-action="save-settings"]').click();
  await waitFor(async () => (await state()).config.servers.find((item) => item.id === "server")?.label === "Evidence connection", "explicit Save persistence");

  await page.locator('.settings-head [data-action="close"]').click();
  const fixtureAgents = (await state()).config.agents.map((agent) => ({ ...agent, b: "server" }));
  await configure({ agents: fixtureAgents });
  await waitFor(async () => (await state()).config.agents[0]?.b === "server", "test-fixture profile binding");
  const beforeSessions = Object.keys((await state()).sessions);
  await page.locator(".agent-tab-new").click();
  await page.locator(".shell-new-choice").first().click();
  const used = await waitFor(async () => {
    const current = await state();
    return Object.values(current.sessions).find((session) => !beforeSessions.includes(session.id) && session.server_id === "server") || null;
  }, "new chat using added connection");
  const persistedDocument = JSON.parse(await readFile(configPath, "utf8"));
  const output = {
    schema: 1, measured_at: new Date().toISOString(), build: initial.build,
    add: { control: "Add connection", persisted_immediately: !!persistedAfterAdd },
    unsaved_test: { visible_state: unsaved, button_enabled: true, saved_before_probe: savedByTest.base_url === `http://127.0.0.1:${modelPort}` },
    test_success: { visible_state: successState, context_size: Number(contextValue) },
    test_failure: { visible_state: failureState },
    explicit_save: { persisted_label: persistedDocument.servers.find((item) => item.id === "server")?.label },
    use: { binding_setup: "test fixture via existing config API; item 2av remains excluded", agent_profile: persistedDocument.agents[0]?.b, new_session_id: used.id, new_session_profile: used.server_id },
  };
  await mkdir(resolve(args.evidence), { recursive: true });
  await writeFile(resolve(args.evidence, "connection-flow.json"), JSON.stringify(output, null, 2));
  process.stdout.write(`${JSON.stringify(output, null, 2)}\n`);
} finally {
  try { await browser?.close(); } catch {}
  try { model.closeAllConnections?.(); model.close(); } catch {}
  for (const child of children.reverse()) try { child.kill(); } catch {}
}
