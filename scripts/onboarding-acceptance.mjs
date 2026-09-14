import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { access, readFile, readdir } from "node:fs/promises";
import { join } from "node:path";
import { chromium } from "playwright";

const args = Object.fromEntries(Array.from({ length: Math.floor(process.argv.slice(2).length / 2) }, (_, index) => {
  const offset = index * 2 + 2;
  return [process.argv[offset].replace(/^--/, ""), process.argv[offset + 1]];
}));
for (const key of ["app", "data", "config", "port"]) assert.ok(args[key], `missing --${key}`);

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const waitJSON = async (url, timeout = 15000) => {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url);
      if (response.ok) return await response.json();
    } catch {}
    await sleep(50);
  }
  throw new Error(`timed out waiting for ${url}`);
};
const stopChild = async (child) => {
  if (!child) return;
  process.stdout.write(`Stopping disposable Agent_b PID ${child.pid} (exitCode=${child.exitCode})\n`);
  if (child.exitCode !== null) return;
  if (process.platform === "win32") {
    const taskkill = spawn(`${process.env.SystemRoot}\\System32\\taskkill.exe`, ["/PID", String(child.pid), "/T", "/F"], {
      windowsHide: true,
      stdio: "ignore",
    });
    const taskkillExit = await new Promise((resolve) => taskkill.once("exit", resolve));
    if (taskkillExit !== 0) throw new Error(`taskkill failed for child process ${child.pid} with exit ${taskkillExit}`);
  } else {
    child.kill("SIGTERM");
  }
  for (let attempt = 0; attempt < 100 && child.exitCode === null; attempt += 1) await sleep(50);
  if (child.exitCode === null) throw new Error(`child process ${child.pid} did not stop`);
};

let requestCount = 0;
const fake = createServer(async (request, response) => {
  requestCount += 1;
  response.setHeader("Content-Type", "application/json");
  if (request.url === "/props") return void response.end(JSON.stringify({ server: "llama.cpp", n_ctx: 32768 }));
  if (request.url === "/v1/models") return void response.end(JSON.stringify({ data: [{ id: "onboarding-fake" }] }));
  let raw = "";
  for await (const chunk of request) raw += chunk;
  const body = raw ? JSON.parse(raw) : {};
  if (request.url === "/tokenize") return void response.end(JSON.stringify({ tokens: [1, 2, 3] }));
  if (request.url === "/apply-template") return void response.end(JSON.stringify({ prompt: JSON.stringify({ messages: body.messages || [], tools: body.tools || [] }) }));
  if (request.url !== "/v1/chat/completions") {
    response.statusCode = 404;
    return void response.end(JSON.stringify({ error: "not found" }));
  }
  if (JSON.stringify(body.messages || []).length > 100000) {
    response.statusCode = 400;
    return void response.end(JSON.stringify({ error: "context length exceeds token limit" }));
  }
  if (body.tool_choice === "required" && (body.tools || []).some((tool) => tool?.function?.name === "read_file")) {
    return void response.end(JSON.stringify({ choices: [{ message: { content: "", tool_calls: [{ id: "onboarding-probe-read", type: "function", function: { name: "read_file", arguments: "{\"path\":\"main.go\"}" } }] }, finish_reason: "tool_calls" }], usage: { prompt_tokens: 10, completion_tokens: 3 } }));
  }
  if (body.stream) {
    response.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
    return void response.end(`data: ${JSON.stringify({ choices: [{ delta: { content: "onboarding fake response" }, finish_reason: "stop" }], usage: { prompt_tokens: 10, completion_tokens: 3 } })}\n\ndata: [DONE]\n\n`);
  }
  response.end(JSON.stringify({ choices: [{ message: { content: "onboarding fake response" }, finish_reason: "stop" }], usage: { prompt_tokens: 10, completion_tokens: 3 } }));
});
await new Promise((resolve) => fake.listen(0, "127.0.0.1", resolve));
const fakePort = fake.address().port;
const baseURL = `http://127.0.0.1:${args.port}`;

const initial = JSON.parse(await readFile(args.config, "utf8"));
assert.deepEqual(initial.servers, [], "fresh disposable install must start with servers:[]");
assert.deepEqual(initial.agents, [], "fresh disposable install must start with agents:[]");

const app = spawn(join(args.app, "Agent_b.exe"), ["-config", args.config, "-app-root", args.app, "-data-root", args.data], {
  windowsHide: true,
  stdio: ["ignore", "pipe", "pipe"],
});
app.stdout.on("data", (chunk) => process.stdout.write(chunk));
app.stderr.on("data", (chunk) => process.stderr.write(chunk));
let browser;
try {
  await waitJSON(`${baseURL}/api/state`);
  await assert.rejects(access(join(args.data, "logs", "retention-expired-working.jsonl")), "expired working JSONL must be pruned on startup");
  await access(join(args.data, "logs", "evidence", "retention-expired-evidence.jsonl"));
  await access(join(args.data, "chats", "retention-retained-chat.jsonl"));
  const startupEvents = (await readdir(join(args.data, "logs"))).filter((name) => name.endsWith(".jsonl"));
  const retentionEvents = [];
  for (const name of startupEvents) {
    for (const line of (await readFile(join(args.data, "logs", name), "utf8")).split(/\r?\n/).filter(Boolean)) {
      const event = JSON.parse(line);
      if (event.type === "log.retention") retentionEvents.push(event);
    }
  }
  assert.ok(retentionEvents.some((event) => event.data?.days === 1 && event.data?.files?.includes("retention-expired-working.jsonl")), "startup tape must record the expired working log");
  browser = await chromium.launch({ channel: "msedge", headless: true });
  const page = await browser.newPage({ viewport: { width: 1250, height: 975 } });
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(String(error.stack || error)));
  await page.goto(`${baseURL}/chat`);
  await page.waitForURL(/\/setup$/);
  await page.locator(".setup-card").filter({ hasText: "API only" }).click();
  await page.locator('[data-field="url"]').fill(`http://127.0.0.1:${fakePort}`);
  await page.locator('[data-field="model"]').fill("onboarding-fake");
  await page.locator('[data-action="test"]').click();
  await page.locator(".setup-feedback").filter({ hasText: "Test complete." }).waitFor({ timeout: 60000 });
  await page.locator('[data-action="continue"]').click();
  await page.locator("h1").filter({ hasText: "Setup is complete" }).waitFor();
  await page.locator('[data-action="finish"]').click();
  await page.waitForURL(/\/chat\?session=main$/);
  await page.locator("#chat-task").fill("acceptance: first-run API-only chat");
  await page.locator("#chat-send").click();
  await page.waitForFunction(() => document.querySelector("#chat-log")?.innerText.includes("onboarding fake response"), undefined, { timeout: 15000 });
  const state = await waitJSON(`${baseURL}/api/state`);
  assert.equal(state.config.servers?.length, 1);
  assert.equal(state.config.agents?.[0]?.b, "setup-api");
  assert.equal(Object.keys(state.sessions || {}).length, 1);
  assert.deepEqual(pageErrors, []);
  process.stdout.write(`PASS: one-day startup retention preserved evidence/chat and fresh servers:[] install reached a working API-only chat through Setup (${requestCount} fake requests)\n`);
} finally {
  await browser?.close().catch(() => {});
  await stopChild(app);
  await new Promise((resolve) => fake.close(resolve));
}
