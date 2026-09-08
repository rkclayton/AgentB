import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readFile } from "node:fs/promises";
import { createServer } from "node:net";
import { join } from "node:path";

const args = Object.fromEntries(Array.from({ length: Math.floor(process.argv.slice(2).length / 2) }, (_, index) => {
  const offset = index * 2 + 2;
  return [process.argv[offset].replace(/^--/, ""), process.argv[offset + 1]];
}));
for (const key of ["app", "data", "replay"]) assert.ok(args[key], `missing --${key}`);
const config = JSON.parse(await readFile(join(args.data, "harness.json"), "utf8"));
const port = Number(String(config.listen).split(":").at(-1));
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const freePort = async () => {
  const probe = createServer();
  await new Promise((resolve) => probe.listen(0, "127.0.0.1", resolve));
  const value = probe.address().port;
  await new Promise((resolve) => probe.close(resolve));
  return value;
};
const browserPort = await freePort();
const json = async (url) => {
  const response = await fetch(url);
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
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
    return result.result.value;
  }
}

const children = [];
let browser;
try {
  const app = spawn(join(args.app, "Agent_b.exe"), ["-config", join(args.data, "harness.json"), "-app-root", args.app, "-data-root", args.data, "-replay", args.replay], { windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
  children.push(app);
  app.stdout.on("data", (chunk) => process.stdout.write(chunk));
  app.stderr.on("data", (chunk) => process.stderr.write(chunk));
  const state = await waitHTTP(`http://127.0.0.1:${port}/api/state`);
  const session = state.sessions.main;
  assert.ok(session, "operator main session missing from replay");
  assert.equal(session.chat.length, 19, "operator main tape must project 19 entries");
  const edge = process.env["ProgramFiles(x86)"] ? join(process.env["ProgramFiles(x86)"], "Microsoft", "Edge", "Application", "msedge.exe") : "msedge.exe";
  const edgeProcess = spawn(edge, ["--headless=new", `--remote-debugging-port=${browserPort}`, `--user-data-dir=${join(args.data, "edge-replay")}`, "--no-first-run", "--disable-extensions", `--app=http://127.0.0.1:${port}/chat?session=main&instant=1`], { windowsHide: true, stdio: "ignore" });
  children.push(edgeProcess);
  let target;
  for (let attempt = 0; attempt < 200; attempt++) {
    try { target = (await json(`http://127.0.0.1:${browserPort}/json/list`)).find((item) => item.type === "page"); if (target) break; } catch {}
    await sleep(50);
  }
  assert.ok(target?.webSocketDebuggerUrl, "Edge DevTools target did not appear");
  browser = new CDP(target.webSocketDebuggerUrl);
  await browser.call("Runtime.enable");
  const deadline = Date.now() + 15000;
  let result;
  while (Date.now() < deadline) {
    result = await browser.evaluate(`({ rows: document.querySelectorAll('[data-entry-key]').length, failures: document.querySelectorAll('.chat-render-failure').length, text: document.querySelector('#chat-log')?.innerText || '' })`);
    if (result.rows === 19) break;
    await sleep(50);
  }
  assert.equal(result.rows, 19, JSON.stringify(result));
  assert.equal(result.failures, 1, JSON.stringify(result));
  assert.match(result.text, /tool .+ could not render · tool arguments are missing or are not an object/);
  process.stdout.write("PASS operator-main-19-entry-replay\n");
} finally {
  try { browser?.ws?.close(); } catch {}
  for (const child of children.reverse()) { try { child.kill(); } catch {} }
  await sleep(250);
}
