import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { copyFile, mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { chromium } from "@playwright/test";

const argv = process.argv.slice(2);
const args = Object.fromEntries(Array.from({ length: Math.floor(argv.length / 2) }, (_, index) => [argv[index * 2].replace(/^--/, ""), argv[index * 2 + 1]]));
for (const name of ["exe", "app-root", "evidence", "expected-commit"]) assert.ok(args[name], `missing --${name}`);
const trials = Number(args.trials || 3);
const postCadenceSeconds = Number(args["post-cadence-seconds"] || 70);
assert.ok(Number.isInteger(trials) && trials > 0 && trials <= 10, "--trials must be 1..10");
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

function spawnPowerShell(parameters) {
  return spawn("powershell.exe", ["-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", resolve("scripts/navigation-network-clicks.ps1"), ...parameters], { windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
}

function collectProcess(child) {
  return new Promise((done, fail) => {
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => { stdout += String(chunk); });
    child.stderr.on("data", (chunk) => { stderr += String(chunk); });
    child.on("error", fail);
    child.on("exit", (code) => code === 0 ? done(JSON.parse(stdout.trim().split(/\r?\n/).at(-1))) : fail(new Error(`coordinate driver exited ${code}: ${stderr || stdout}`)));
  });
}

async function waitFor(check, label, timeout = 20000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    try {
      const value = await check();
      if (value) return value;
    } catch {}
    await sleep(25);
  }
  throw new Error(`${label} timeout`);
}

function navigationID(url) {
  try { return new URL(url).searchParams.get("navigation_id") || ""; } catch { return ""; }
}

const tempRoot = await mkdtemp(join(tmpdir(), "Agent_b-navigation-network-"));
const evidenceRoot = resolve(args.evidence);
const dataRoot = join(tempRoot, "data");
const workspace = join(dataRoot, "workspace");
const toolset = ["read_file", "list_dir", "write_file", "edit_file", "search_text", "shell", "remember", "recall", "fetch_url", "find_files", "run_script", "call_service"];
const children = [];
let model;
let app;
let appStderr = "";

async function stopChild(child) {
  if (!child || child.exitCode !== null) return;
  child.kill();
  await Promise.race([new Promise((done) => child.once("exit", done)), sleep(3000)]);
}

try {
  await mkdir(workspace, { recursive: true });
  await mkdir(evidenceRoot, { recursive: false });
  const modelPort = await freePort();
  model = createServer(async (request, response) => {
    if (request.url === "/props") return void response.end(JSON.stringify({ server: "navigation-network-evidence", n_ctx: 32768 }));
    if (request.url === "/v1/models") return void response.end(JSON.stringify({ data: [{ id: "navigation-network-evidence" }] }));
    if (request.url === "/tokenize") return void response.end(JSON.stringify({ tokens: [1] }));
    if (request.url === "/apply-template") return void response.end(JSON.stringify({ prompt: "navigation network evidence" }));
    for await (const _chunk of request) { /* drain */ }
    response.statusCode = 404;
    response.end();
  });
  await new Promise((done) => model.listen(modelPort, "127.0.0.1", done));

  const appPort = await freePort();
  const config = {
    config_version: 6, listen: `127.0.0.1:${appPort}`, workspace, log_dir: join(dataRoot, "logs"),
    servers: [{ id: "navigation", label: "Navigation", base_url: `http://127.0.0.1:${modelPort}`, model: "navigation-network-evidence", credential: "", request_timeout_s: 3, probe_mode: "off",
      sampling: { thinking: { temperature: .6, top_p: .95, top_k: 20, min_p: 0, presence_penalty: 0, repeat_penalty: 1 }, nonthinking: { temperature: .7, top_p: .8, top_k: 20, min_p: 0, presence_penalty: 0, repeat_penalty: 1 } },
      reasoning: { control: "auto", enabled: false, effort: "medium", valid_efforts: [], preserve: false }, context: { n_ctx: 32768, reserve_output: 10240 }, system_prompt_override: "",
      capabilities: { server: "navigation-network-evidence", props: true, n_ctx: 32768, tokenize: true, apply_template: true, apply_template_tools: true, streaming: true, tool_calls: true, grammar_constrained: false, cached_tokens: true, timings: false, prompt_progress: false, document_input: false, image_input: false, reasoning_control: "", valid_efforts: [], overflow_behavior: "error", probed_at: new Date().toISOString(), findings: ["navigation network evidence fixture"] } }],
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
  app = spawn(resolve(args.exe), ["-config", configPath, "-app-root", resolve(args["app-root"]), "-data-root", dataRoot], { windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
  children.push(app);
  app.stderr.on("data", (chunk) => { appStderr += String(chunk); });
  const base = `http://127.0.0.1:${appPort}`;
  const state = async () => {
    const response = await fetch(`${base}/api/state`, { cache: "no-store" });
    assert.equal(response.status, 200);
    return response.json();
  };
  const initial = await waitFor(state, "candidate startup");
  assert.equal(initial.build.commit, args["expected-commit"], "running executable commit does not match requested build");
  assert.equal(initial.build.dirty, false, "running evidence build must be clean-stamped");
  assert.ok(initial.sessions.main, "main chat missing");
  const logPath = initial.sessions.main.log_path;
  const readTape = async () => (await readFile(logPath, "utf8")).split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
  const offsets = Array.from({ length: 30 }, (_, index) => index * 50);
  const runs = [];

  for (let trial = 1; trial <= trials; trial += 1) {
    const before = await readTape();
    const debugPort = await freePort();
    const profile = join(tempRoot, `edge-${trial}`);
    const ready = join(tempRoot, `collector-${trial}.ready`);
    const initialURL = `${base}/chat?session=main`;
    const driverProcess = spawnPowerShell(["-Url", initialURL, "-OffsetsJson", JSON.stringify(offsets), "-RemoteDebuggingPort", String(debugPort), "-UserDataDirectory", profile, "-CollectorReadyPath", ready, "-PostCadenceSeconds", String(postCadenceSeconds)]);
    children.push(driverProcess);
    const driverPromise = collectProcess(driverProcess);
    const endpoint = `http://127.0.0.1:${debugPort}`;
    await waitFor(async () => (await fetch(`${endpoint}/json/version`)).ok, `trial ${trial} remote-debug endpoint`);
    const browser = await chromium.connectOverCDP(endpoint);
    const page = await waitFor(() => browser.contexts().flatMap((context) => context.pages()).find((candidate) => candidate.url().startsWith(base)), `trial ${trial} app page`);
    const cdp = await page.context().newCDPSession(page);
    const network = [];
    const pageEvents = [];
    for (const name of ["requestWillBeSent", "responseReceived", "loadingFinished", "loadingFailed"]) {
      cdp.on(`Network.${name}`, (event) => network.push({ event: name, observed_at: new Date().toISOString(), ...event }));
    }
    for (const name of ["frameNavigated", "domContentEventFired", "loadEventFired", "lifecycleEvent"]) {
      cdp.on(`Page.${name}`, (event) => pageEvents.push({ event: name, observed_at: new Date().toISOString(), ...event }));
    }
    await cdp.send("Page.enable");
    await cdp.send("Page.setLifecycleEventsEnabled", { enabled: true });
    await cdp.send("Network.enable", { maxTotalBufferSize: 100000000, maxResourceBufferSize: 10000000 });
    await writeFile(ready, "ready\n");
    const driver = await driverPromise;
    await sleep(1000);
    await browser.close().catch(() => {});
    const after = await readTape();
    const added = after.slice(before.length);
    const started = added.filter((event) => event.type === "navigation.started");
    const measured = added.filter((event) => event.type === "navigation.measured");
    const documentStarted = added.filter((event) => event.type === "navigation.document_started" && event.data?.navigation_id);
    const documentCompleted = added.filter((event) => event.type === "navigation.document_completed" && event.data?.navigation_id);
    const completedIDs = new Set(measured.map((event) => event.data?.navigation_id));
    const requests = network.filter((event) => event.event === "requestWillBeSent" && event.type === "Document" && navigationID(event.request?.url));
    const responseByRequest = new Map(network.filter((event) => event.event === "responseReceived").map((event) => [event.requestId, event]));
    const finishedByRequest = new Map(network.filter((event) => event.event === "loadingFinished").map((event) => [event.requestId, event]));
    const failedByRequest = new Map(network.filter((event) => event.event === "loadingFailed").map((event) => [event.requestId, event]));
    const frameNavigated = pageEvents.filter((event) => event.event === "frameNavigated");
    const lifecycle = pageEvents.filter((event) => event.event === "lifecycleEvent");
    const correlations = started.map((start) => {
      const id = start.data?.navigation_id;
      const request = requests.find((event) => navigationID(event.request?.url) === id);
      const response = request ? responseByRequest.get(request.requestId) : undefined;
      const finished = request ? finishedByRequest.get(request.requestId) : undefined;
      const failed = request ? failedByRequest.get(request.requestId) : undefined;
      const serverStart = documentStarted.find((event) => event.data?.navigation_id === id);
      const serverComplete = documentCompleted.find((event) => event.data?.navigation_id === id);
      const committed = request ? frameNavigated.find((event) => event.frame?.loaderId === request.loaderId && navigationID(event.frame?.url) === id) : undefined;
      const domContentLoaded = request ? lifecycle.find((event) => event.loaderId === request.loaderId && event.name === "DOMContentLoaded") : undefined;
      const load = request ? lifecycle.find((event) => event.loaderId === request.loaderId && event.name === "load") : undefined;
      return {
        navigation_id: id,
        app_started_at: start.ts,
        app_completed: completedIDs.has(id),
        network_request: request ? { request_id: request.requestId, loader_id: request.loaderId, timestamp: request.timestamp, wall_time: request.wallTime, url: request.request.url } : null,
        network_response: response ? { timestamp: response.timestamp, status: response.response?.status, protocol: response.response?.protocol, connection_id: response.response?.connectionId, connection_reused: response.response?.connectionReused, remote_ip_address: response.response?.remoteIPAddress, remote_port: response.response?.remotePort, timing: response.response?.timing } : null,
        network_loading_finished: finished ? { timestamp: finished.timestamp, encoded_data_length: finished.encodedDataLength } : null,
        network_loading_failed: failed ? { timestamp: failed.timestamp, error_text: failed.errorText, canceled: failed.canceled, blocked_reason: failed.blockedReason } : null,
        navigation_committed: Boolean(committed),
        page_frame_navigated: committed ? { observed_at: committed.observed_at, frame_id: committed.frame?.id, loader_id: committed.frame?.loaderId, url: committed.frame?.url, navigation_type: committed.type || "" } : null,
        page_dom_content_loaded: domContentLoaded ? { observed_at: domContentLoaded.observed_at, timestamp: domContentLoaded.timestamp, frame_id: domContentLoaded.frameId, loader_id: domContentLoaded.loaderId } : null,
        page_load_event: load ? { observed_at: load.observed_at, timestamp: load.timestamp, frame_id: load.frameId, loader_id: load.loaderId } : null,
        server_started: serverStart?.data || null,
        server_completed: serverComplete?.data || null,
      };
    });
    const run = {
      trial,
      driver,
      app: { started_count: started.length, completed_count: measured.length, incomplete_count: correlations.filter((item) => !item.app_completed).length },
      network: { request_count: requests.length, response_count: correlations.filter((item) => item.network_response).length, loading_finished_count: correlations.filter((item) => item.network_loading_finished).length, loading_failed_count: correlations.filter((item) => item.network_loading_failed).length },
      page: { frame_navigated_count: correlations.filter((item) => item.page_frame_navigated).length, dom_content_loaded_count: correlations.filter((item) => item.page_dom_content_loaded).length, load_event_count: correlations.filter((item) => item.page_load_event).length, raw_dom_content_event_count: pageEvents.filter((event) => event.event === "domContentEventFired").length, raw_load_event_count: pageEvents.filter((event) => event.event === "loadEventFired").length },
      correlations,
      raw_network_events: network,
      raw_page_events: pageEvents,
    };
    runs.push(run);
    await writeFile(join(evidenceRoot, `trial-${trial}.json`), JSON.stringify(run, null, 2));
  }

  const reproduction = {
    incomplete: runs.reduce((sum, run) => sum + run.app.incomplete_count, 0),
    started: runs.reduce((sum, run) => sum + run.app.started_count, 0),
    trials_with_incomplete: runs.filter((run) => run.app.incomplete_count > 0).length,
  };
  const retainedTape = join(evidenceRoot, "server-tape.jsonl");
  await copyFile(logPath, retainedTape);
  const output = {
    schema: 2,
    measured_at: new Date().toISOString(),
    build: initial.build,
    fidelity: {
      production_browser_arguments: ["--app=<url>"],
      evidence_browser_arguments: ["--app=<disposable-url>", "--remote-debugging-port=<port>", "--remote-allow-origins=*", "--user-data-dir=<isolated-temp-profile>", "--no-first-run", "--no-default-browser-check", "--disable-background-mode"],
      same_coordinate_driver_logic: true,
      original_coordinate_harness_unchanged: true,
      differences: ["remote debugging and its required isolated profile", "disposable Agent_b server, data root, model endpoint, and URL/port", "window moved after launch to make a stable physical coordinate"],
    },
    w1_reproduction_from_app_tape: { ...reproduction, reproduced: reproduction.trials_with_incomplete > 0 },
    runs,
    tape: { path: retainedTape, source_path: logPath },
  };
  await writeFile(join(evidenceRoot, "raw.json"), JSON.stringify(output, null, 2));
  process.stdout.write(`${JSON.stringify({ build: output.build, fidelity: output.fidelity, reproduction_from_app_tape: output.w1_reproduction_from_app_tape, trials: runs.map((run) => ({ trial: run.trial, app: run.app, network: run.network, page: run.page })) }, null, 2)}\n`);
} catch (error) {
  throw new Error(`${error.stack || error}\nAgent_b stderr:\n${appStderr}`);
} finally {
  await Promise.all(children.map(stopChild));
  if (model) {
    model.closeAllConnections?.();
    await new Promise((done) => model.close(done)).catch(() => {});
  }
  const expectedPrefix = join(tmpdir(), "Agent_b-navigation-network-");
  if (tempRoot.startsWith(expectedPrefix)) await rm(tempRoot, { recursive: true, force: true });
}
