import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { chromium } from "playwright";

const argv = process.argv.slice(2);
const args = Object.fromEntries(Array.from({ length: Math.floor(argv.length / 2) }, (_, index) => [argv[index * 2].replace(/^--/, ""), argv[index * 2 + 1]]));
for (const name of ["exe", "app-root", "data", "evidence"]) assert.ok(args[name], `missing --${name}`);
const intervalMS = Number(args["interval-ms"] || 333);
const maximumClicks = Number(args["maximum-clicks"] || 40);
const settlingMS = Number(args["settling-ms"] || 10000);
const modelCondition = args["model-condition"] || "reachable";
assert.ok(["reachable", "unreachable"].includes(modelCondition), "--model-condition must be reachable or unreachable");
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

const model = createServer(async (request, response) => {
  if (request.url === "/props") return void response.end(JSON.stringify({ server: "navigation-rapid-evidence", n_ctx: 32768 }));
  if (request.url === "/v1/models") return void response.end(JSON.stringify({ data: [{ id: "navigation-rapid-evidence" }] }));
  if (request.url === "/tokenize") return void response.end(JSON.stringify({ tokens: [1] }));
  if (request.url === "/apply-template") return void response.end(JSON.stringify({ prompt: "navigation rapid evidence" }));
  for await (const _chunk of request) { /* drain */ }
  response.statusCode = 404;
  response.end();
});

const children = [];
let browser;
let stderr = "";
async function cleanup() {
  try { await browser?.close(); } catch {}
  try { model.closeAllConnections?.(); model.close(); } catch {}
  for (const child of children.reverse()) {
    try { child.kill(); } catch {}
  }
}
process.on("exit", () => {
  try { model.closeAllConnections?.(); model.close(); } catch {}
  for (const child of children.reverse()) try { child.kill(); } catch {}
});

const modelPort = await freePort();
await new Promise((done) => model.listen(modelPort, "127.0.0.1", done));
const appPort = await freePort();
const dataRoot = resolve(args.data);
const workspace = join(dataRoot, "workspace");
await mkdir(workspace, { recursive: true });
const toolset = ["read_file", "list_dir", "write_file", "edit_file", "search_text", "shell", "remember", "recall", "fetch_url", "find_files", "run_script", "call_service"];
const config = {
  config_version: 6, listen: `127.0.0.1:${appPort}`, workspace, log_dir: join(dataRoot, "logs"),
  servers: [{ id: "navigation", label: "Navigation", base_url: `http://127.0.0.1:${modelPort}`, model: "navigation-rapid-evidence", credential: "", request_timeout_s: 3, probe_mode: "off",
    sampling: { thinking: { temperature: .6, top_p: .95, top_k: 20, min_p: 0, presence_penalty: 0, repeat_penalty: 1 }, nonthinking: { temperature: .7, top_p: .8, top_k: 20, min_p: 0, presence_penalty: 0, repeat_penalty: 1 } },
    reasoning: { control: "auto", enabled: false, effort: "medium", valid_efforts: [], preserve: false }, context: { n_ctx: 32768, reserve_output: 10240 }, system_prompt_override: "",
    capabilities: { server: "navigation-rapid-evidence", props: true, n_ctx: 32768, tokenize: true, apply_template: true, apply_template_tools: true, streaming: true, tool_calls: true, grammar_constrained: false, cached_tokens: true, timings: false, prompt_progress: false, document_input: false, image_input: false, reasoning_control: "", valid_efforts: [], overflow_behavior: "error", probed_at: new Date().toISOString(), findings: ["rapid navigation evidence fixture"] } }],
  services: {}, agents: [{ name: "Navigation", b: "navigation", toolset }], chat: { auto_rename: false },
  run: { max_turns: 4, cycle_window: 8, max_consecutive_tool_errors: 3, max_concurrent: 1, queue_depth: 0 }, approval: { mode: "boundary-only" },
  deliver: { mode: "chips", exchange_folder: join(dataRoot, "exchange") }, operator_files: { allow_mailbox_approvals: false, log_retention_days: 30 },
  context: { soft_pct: .75, summary_pct: .95, accounting: modelCondition === "unreachable" ? "exact" : "estimated" }, memory: { enabled: false, dir: join(dataRoot, "memory"), max_tokens: 1500 },
  tools: { read_file: { default_limit: 16384, max_limit: 65536 }, attachments: { max_bytes: 8388608 }, list_dir: { max_entries: 300, ignore: [".git"] }, grep: { max_matches: 50, max_line_chars: 200 }, shell: { operator_commands: [] }, fetch: { timeout_s: 20, max_bytes: 2097152, max_redirects: 5, default_limit: 16384, max_limit: 65536, allow_domains: [], deny_domains: [], allow_internal_hosts: [] }, find_files: { skip_roots: [] } },
  shell: { command: ["powershell", "-NoProfile", "-NonInteractive", "-Command"], timeout_s: 60, max_timeout_s: 600, max_output_lines_head: 60, max_output_lines_tail: 40, file_routing_guard: true, operator_context: false, operator_context_idle_timeout_minutes: 20, service_account: { enabled: false, account: "agentb-svc", domain: "." }, deny: [] },
  signing: { thumbprint: "", timestamp_url: "http://timestamp.digicert.com" },
};
const configPath = join(dataRoot, "harness.json");
await writeFile(configPath, JSON.stringify(config, null, 2));

try {
  const app = spawn(resolve(args.exe), ["-config", configPath, "-app-root", resolve(args["app-root"]), "-data-root", dataRoot], { windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
  children.push(app);
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
  assert.ok(initial.sessions.main, "main chat missing");
  if (modelCondition === "unreachable") {
    model.closeAllConnections?.();
    await new Promise((done) => model.close(done));
    const response = await fetch(`${base}/api/sessions/main`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-AgentB-Mutation-Token": initial.mutation_token },
      body: JSON.stringify({ agent_id: "navigation" }),
    });
    assert.equal(response.status, 200, await response.text());
    await waitFor(async () => (await state()).sessions.main?.model_unreachable, "unreachable projection");
  }
  const logPath = initial.sessions.main.log_path;
  async function events() {
    const text = await readFile(logPath, "utf8");
    return text.split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
  }
  async function count(type) { return (await events()).filter((event) => event.type === type).length; }
  async function waitForRecord(type, prior, timeout = 15000) {
    return waitFor(async () => {
      const selected = (await events()).filter((event) => event.type === type);
      return selected.length > prior ? selected.at(-1) : null;
    }, `${type} ${prior + 1}`, timeout);
  }

  browser = await chromium.launch({ channel: "msedge", headless: true });
  const context = await browser.newContext({ viewport: { width: 1250, height: 975 } });
  const page = await context.newPage();
  await page.goto(`${base}/chat?session=main`);

  const clicks = [];
  async function flip(label, waitForCompletion = true) {
    await page.locator('.agent-tab[data-agent="agent_b"]').waitFor({ timeout: 60000 });
    const startsBefore = await count("navigation.started");
    const measuredBefore = await count("navigation.measured");
    const requestedAt = Date.now();
    await page.locator('.agent-tab[data-agent="agent_b"]').click({ noWaitAfter: true, timeout: 5000 });
    const started = await waitForRecord("navigation.started", startsBefore);
    let measured = null;
    if (waitForCompletion) {
      try {
        measured = await waitFor(async () => (await events()).find((event) => event.type === "navigation.measured" && event.data?.navigation_id === started.data?.navigation_id) || null, `${label} completion`, 70000);
      } catch {}
    }
    clicks.push({
      label,
      requested_at: new Date(requestedAt).toISOString(),
      request_interval_ms: clicks.length ? requestedAt - Date.parse(clicks.at(-1).requested_at) : null,
      navigation_id: started.data.navigation_id,
      subscriber_count: started.data.subscriber_count,
      started_ts: started.ts,
      completed_ts: measured?.ts || null,
      completion: measured?.data || null,
      measured_count_before: measuredBefore,
    });
    return clicks.at(-1);
  }

  const baseline = await flip("baseline");
  assert.ok(baseline.completion, "baseline navigation did not complete");
  await sleep(2000);

  const rapid = [];
  let degradation = null;
  for (let index = 1; index <= maximumClicks; index++) {
    const priorRequest = clicks.length ? Date.parse(clicks.at(-1).requested_at) : 0;
    await sleep(Math.max(0, intervalMS - (Date.now() - priorRequest)));
    const sample = await flip(`rapid-${index}`);
    rapid.push(sample);
    if (!sample.completion) {
      degradation = { kind: "completion_timeout", click: index, threshold_ms: 70000 };
      break;
    }
    const recent = rapid.slice(-4).map((entry) => entry.completion);
    if (recent.length === 4) {
      const rebuild = recent.map((entry) => entry.transcript_surface_rebuild_paint_ms);
      const connect = recent.map((entry) => entry.event_stream_connect_ms);
      const rising = (values) => values.every((value, offset) => offset === 0 || value >= values[offset - 1]);
      if (rising(rebuild) && rising(connect) && (rebuild.at(-1) >= rebuild[0] * 1.5 || connect.at(-1) >= connect[0] * 1.5)) {
        degradation = { kind: "four_sample_growth", click: index, rebuild, connect };
        break;
      }
    }
  }

  await sleep(settlingMS);
  const settled = await flip("settled-probe");
  assert.ok(settled.completion, "settled probe navigation did not complete");
  const allEvents = await events();
  const starts = allEvents.filter((event) => event.type === "navigation.started");
  assert.equal(starts.length, clicks.length, "one start record is required per click");
  const rapidCounts = rapid.map((entry) => Number(entry.subscriber_count));
  const strictlyRises = rapidCounts.length > 1 && rapidCounts.every((value, index) => index === 0 || value > rapidCounts[index - 1]);
  const didNotSettle = Number(settled.subscriber_count) > Number(baseline.subscriber_count);
  const repairBarMet = strictlyRises && didNotSettle;
  const output = {
    schema: 1,
    measured_at: new Date().toISOString(),
    build: initial.build,
    parameters: { model_condition: modelCondition, interval_ms: intervalMS, maximum_clicks: maximumClicks, settling_ms: settlingMS, degradation_rule: "completion exceeds 70s, or four consecutive completed samples have nondecreasing rebuild and connect with at least one growing 1.5x" },
    baseline,
    rapid,
    degradation,
    settled_probe: settled,
    repair_bar: { strictly_rising_across_rapid_sequence: strictlyRises, returned_to_pre_sequence_level: !didNotSettle, met: repairBarMet },
    tape: { path: logPath, navigation_started: starts.length, navigation_measured: allEvents.filter((event) => event.type === "navigation.measured").length },
  };
  await mkdir(resolve(args.evidence), { recursive: true });
  await writeFile(join(resolve(args.evidence), "raw.json"), JSON.stringify(output, null, 2));
  process.stdout.write(`${JSON.stringify(output, null, 2)}\n`);
} finally {
  await cleanup();
}
