import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { chromium } from "playwright";

const argv = process.argv.slice(2);
const args = Object.fromEntries(Array.from({ length: Math.floor(argv.length / 2) }, (_, index) => [argv[index * 2].replace(/^--/, ""), argv[index * 2 + 1]]));
for (const name of ["baseline-exe", "baseline-root", "candidate-exe", "candidate-root", "data", "evidence", "candidate-commit"]) assert.ok(args[name], `missing --${name}`);
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

const profile = {
  id: "local", label: "Local model", base_url: "http://127.0.0.1:8000", extract_url: "", model: "example-model", credential: "", request_timeout_s: 900, probe_mode: "full",
  sampling: { thinking: { temperature: .6, top_p: .95, top_k: 20, min_p: 0, presence_penalty: 0, repeat_penalty: 1 }, nonthinking: { temperature: .7, top_p: .8, top_k: 20, min_p: 0, presence_penalty: 1.5, repeat_penalty: 1 } },
  reasoning: { control: "auto", enabled: true, effort: "medium", valid_efforts: [], preserve: false }, context: { n_ctx: 32768, reserve_output: 10240 }, system_prompt_override: "",
  capabilities: { server: "llama.cpp", props: true, n_ctx: 32768, tokenize: true, apply_template: true, apply_template_tools: true, streaming: true, tool_calls: true, grammar_constrained: false, cached_tokens: true, timings: true, prompt_progress: false, document_input: false, image_input: false, reasoning_control: "none", valid_efforts: [], overflow_behavior: "error", probed_at: "2026-09-11T12:00:00Z", findings: ["screenshot fixture"] },
};

async function capture(name, exe, appRoot, expected) {
  const port = await freePort();
  const dataRoot = resolve(args.data, name);
  const workspace = join(dataRoot, "workspace");
  await mkdir(workspace, { recursive: true });
  const config = { config_version: 6, listen: `127.0.0.1:${port}`, workspace, log_dir: join(dataRoot, "logs"), servers: [profile], services: {}, agents: [{ name: "Screenshot", b: "local", toolset: ["read_file", "list_dir", "write_file", "edit_file", "search_text", "shell", "remember", "recall", "fetch_url", "find_files", "run_script", "call_service"] }] };
  const configPath = join(dataRoot, "harness.json");
  await writeFile(configPath, JSON.stringify(config, null, 2));
  const app = spawn(resolve(exe), ["-config", configPath, "-app-root", resolve(appRoot), "-data-root", dataRoot], { windowsHide: true, stdio: ["ignore", "ignore", "pipe"] });
  let stderr = "";
  app.stderr.on("data", (chunk) => { stderr += String(chunk); });
  let browser;
  try {
    const base = `http://127.0.0.1:${port}`;
    let state;
    const deadline = Date.now() + 15000;
    while (!state && Date.now() < deadline) {
      try { const response = await fetch(`${base}/api/state`); if (response.ok) state = await response.json(); } catch {}
      if (!state) await sleep(50);
    }
    assert.ok(state, `${name} startup timed out: ${stderr}`);
    assert.equal(state.build.commit, expected);
    browser = await chromium.launch({ channel: "msedge", headless: true });
    const page = await browser.newPage({ viewport: { width: 1250, height: 975 }, deviceScaleFactor: 1 });
    await page.goto(`${base}/`);
    await page.locator(".shell-settings").click();
    await page.locator("#settings-page").waitFor({ state: "visible" });
    await page.locator('[data-action="profile-toggle"]').first().waitFor({ state: "visible" });
    await page.locator('[data-action="profile-toggle"]').first().click();
    await page.locator('.setting-input[data-path$=".label"]').waitFor({ state: "visible" });
    await page.screenshot({ path: resolve(args.evidence, `${name}.png`) });
    return { build: state.build, metrics: await page.evaluate(() => ({ content_width: document.querySelector(".settings-content").clientWidth, content_scroll_width: document.querySelector(".settings-content").scrollWidth, group_height: document.querySelector(".settings-group").scrollHeight })) };
  } finally {
    try { await browser?.close(); } catch {}
    try { app.kill(); } catch {}
  }
}

await mkdir(resolve(args.evidence), { recursive: true });
const baseline = await capture("before-v0280", args["baseline-exe"], args["baseline-root"], "d8f34df52c4731076834bf99134c486d8675d5bb");
const candidate = await capture("after-v0290", args["candidate-exe"], args["candidate-root"], args["candidate-commit"]);
const output = { schema: 1, measured_at: new Date().toISOString(), baseline, candidate };
await writeFile(resolve(args.evidence, "captures.json"), JSON.stringify(output, null, 2));
process.stdout.write(`${JSON.stringify(output, null, 2)}\n`);
