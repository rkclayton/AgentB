import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const argv = process.argv.slice(2);
const args = Object.fromEntries(Array.from({ length: Math.floor(argv.length / 2) }, (_, index) => [argv[index * 2].replace(/^--/, ""), argv[index * 2 + 1]]));
for (const name of ["exe", "app-root", "data", "evidence", "expected-commit"]) assert.ok(args[name], `missing --${name}`);
const sleep = (ms) => new Promise((done) => setTimeout(done, ms));
const postCadenceSeconds = Number(args["post-cadence-seconds"] || 70);
const navigationGuard = args["navigation-guard"] === "1";

async function freePort() {
  const probe = createServer();
  await new Promise((done) => probe.listen(0, "127.0.0.1", done));
  const port = probe.address().port;
  await new Promise((done) => probe.close(done));
  assert.notEqual(port, 7337);
  assert.notEqual(port, 8790);
  return port;
}

function runPowerShell(parameters) {
  return new Promise((done, fail) => {
    const child = spawn("powershell.exe", ["-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", resolve("scripts/navigation-coordinate-clicks.ps1"), ...parameters], { windowsHide: true });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => { stdout += String(chunk); });
    child.stderr.on("data", (chunk) => { stderr += String(chunk); });
    child.on("error", fail);
    child.on("exit", (code) => code === 0 ? done(JSON.parse(stdout.trim().split(/\r?\n/).at(-1))) : fail(new Error(`coordinate driver exited ${code}: ${stderr || stdout}`)));
  });
}

const patterns = [
  { name: "operator", description: "24 repeated clicks at 250 ms", offsets: Array.from({ length: 24 }, (_, index) => index * 250) },
  { name: "burst", description: "30-click burst at 50 ms", offsets: Array.from({ length: 30 }, (_, index) => index * 50) },
  { name: "bursts-with-pauses", description: "four 6-click 80 ms bursts separated by 1.2 s", offsets: Array.from({ length: 4 }, (_, group) => Array.from({ length: 6 }, (_, index) => group * 1600 + index * 80)).flat() },
];
const selectedPatterns = args.pattern ? patterns.filter((pattern) => pattern.name === args.pattern) : patterns;
assert.ok(selectedPatterns.length, `unknown --pattern ${args.pattern}`);

const model = createServer(async (request, response) => {
  if (request.url === "/props") return void response.end(JSON.stringify({ server: "navigation-coordinate-evidence", n_ctx: 32768 }));
  if (request.url === "/v1/models") return void response.end(JSON.stringify({ data: [{ id: "navigation-coordinate-evidence" }] }));
  if (request.url === "/tokenize") return void response.end(JSON.stringify({ tokens: [1] }));
  if (request.url === "/apply-template") return void response.end(JSON.stringify({ prompt: "navigation coordinate evidence" }));
  for await (const _chunk of request) { /* drain */ }
  response.statusCode = 404;
  response.end();
});

const children = [];
let stderr = "";
async function cleanup() {
  try { model.closeAllConnections?.(); model.close(); } catch {}
  for (const child of children.reverse()) try { child.kill(); } catch {}
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
  servers: [{ id: "navigation", label: "Navigation", base_url: `http://127.0.0.1:${modelPort}`, model: "navigation-coordinate-evidence", credential: "", request_timeout_s: 3, probe_mode: "off",
    sampling: { thinking: { temperature: .6, top_p: .95, top_k: 20, min_p: 0, presence_penalty: 0, repeat_penalty: 1 }, nonthinking: { temperature: .7, top_p: .8, top_k: 20, min_p: 0, presence_penalty: 0, repeat_penalty: 1 } },
    reasoning: { control: "auto", enabled: false, effort: "medium", valid_efforts: [], preserve: false }, context: { n_ctx: 32768, reserve_output: 10240 }, system_prompt_override: "",
    capabilities: { server: "navigation-coordinate-evidence", props: true, n_ctx: 32768, tokenize: true, apply_template: true, apply_template_tools: true, streaming: true, tool_calls: true, grammar_constrained: false, cached_tokens: true, timings: false, prompt_progress: false, document_input: false, image_input: false, reasoning_control: "", valid_efforts: [], overflow_behavior: "error", probed_at: new Date().toISOString(), findings: ["coordinate navigation evidence fixture"] } }],
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
  assert.equal(initial.build.commit, args["expected-commit"], "running executable commit does not match the requested build");
  assert.equal(initial.build.dirty, false, "running evidence build must be clean-stamped");
  assert.ok(initial.sessions.main, "main chat missing");
  const logPath = initial.sessions.main.log_path;
  const evidenceRoot = resolve(args.evidence);
  await mkdir(evidenceRoot, { recursive: true });
  async function navigationEvents() {
    const text = await readFile(logPath, "utf8");
    return text.split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line)).filter((event) => event.type === "navigation.started" || event.type === "navigation.measured" || event.type === "navigation.suppressed");
  }

  const runs = [];
  for (const pattern of selectedPatterns) {
    const before = await navigationEvents();
    const initialURL = `${base}/chat?session=main${navigationGuard ? "&navigation_guard=1" : ""}`;
    const driver = await runPowerShell(["-Url", initialURL, "-OffsetsJson", JSON.stringify(pattern.offsets), "-PostCadenceSeconds", String(postCadenceSeconds)]);
    await writeFile(join(evidenceRoot, `${pattern.name}-driver.json`), JSON.stringify(driver, null, 2));
    await sleep(1000);
    const after = await navigationEvents();
    const added = after.slice(before.length);
    const started = added.filter((event) => event.type === "navigation.started");
    const measured = added.filter((event) => event.type === "navigation.measured");
    const suppressed = added.filter((event) => event.type === "navigation.suppressed");
    assert.ok(started.length > 0, `${pattern.name}: coordinate input did not produce a navigation`);
    const completions = new Map(measured.map((event) => [event.data?.navigation_id, event]));
    let active = 0;
    let maximumInFlight = 0;
    for (const event of added.toSorted((a, b) => Date.parse(a.ts) - Date.parse(b.ts))) {
      if (event.type === "navigation.started") maximumInFlight = Math.max(maximumInFlight, ++active);
      else active = Math.max(0, active - 1);
    }
    const run = {
      pattern: { name: pattern.name, description: pattern.description, intended_offsets_ms: pattern.offsets },
      driver,
      navigation: {
        started: started.map((event) => event.data),
        measured: measured.map((event) => event.data),
        click_count: pattern.offsets.length,
        started_count: started.length,
        measured_count: measured.length,
        suppressed_count: suppressed.length,
        maximum_in_flight: maximumInFlight,
        incomplete_ids: started.map((event) => event.data?.navigation_id).filter((id) => !completions.has(id)),
        maximum_document_request_and_parse_ms: measured.reduce((maximum, event) => Math.max(maximum, Number(event.data?.document_request_parse_ms || event.data?.document_request_and_parse_ms || 0)), 0),
      },
    };
    runs.push(run);
    await writeFile(join(evidenceRoot, `${pattern.name}.json`), JSON.stringify(run, null, 2));
  }

  const reproduced = runs.some((run) => run.driver.responsiveness.some((sample) => !sample.responsive) || run.navigation.incomplete_ids.length || run.navigation.maximum_document_request_and_parse_ms >= 10000);
  const overlap = runs.some((run) => run.navigation.maximum_in_flight > 1);
  const output = {
    schema: 1,
    measured_at: new Date().toISOString(),
    build: initial.build,
    fidelity: {
      production_browser_arguments: ["--app=<url>"],
      evidence_browser_arguments: ["--app=<disposable-url>"],
      same_launch_style: true,
      navigation_guard_enabled: navigationGuard,
      remote_debugging: false,
      differences: ["disposable Agent_b server, data root, model endpoint, and URL/port", "window moved after launch to make a stable physical coordinate"],
    },
    result: reproduced ? (overlap ? "reproduced-with-overlapping-navigation" : "reproduced-without-overlapping-navigation") : "not-reproduced-under-tested-cadences",
    reproduced,
    overlapping_navigation_observed: overlap,
    reproduction_rule: "any 1 s Win32 responsiveness timeout, incomplete navigation after the 70 s observation window, or document request/parse >= 10 s",
    runs,
    tape: { path: logPath },
  };
  await writeFile(join(evidenceRoot, "raw.json"), JSON.stringify(output, null, 2));
  process.stdout.write(`${JSON.stringify(output, null, 2)}\n`);
} finally {
  await cleanup();
}
