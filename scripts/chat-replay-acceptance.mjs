import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { join } from "node:path";
import { chromium } from "@playwright/test";

const args = Object.fromEntries(Array.from({ length: Math.floor(process.argv.slice(2).length / 2) }, (_, index) => {
  const offset = index * 2 + 2;
  return [process.argv[offset].replace(/^--/, ""), process.argv[offset + 1]];
}));
for (const key of ["app", "data", "replay"]) assert.ok(args[key], `missing --${key}`);
const configPath = join(args.data, "harness.json");
const config = JSON.parse(await readFile(configPath, "utf8"));
const portProbe = createServer();
await new Promise((resolve) => portProbe.listen(0, "127.0.0.1", resolve));
const port = portProbe.address().port;
await new Promise((resolve) => portProbe.close(resolve));
config.listen = `127.0.0.1:${port}`;
await writeFile(configPath, `${JSON.stringify(config, null, 2)}\n`);
const tape = await readFile(args.replay, "utf8");
const recordCount = tape.split(/\r?\n/).filter(Boolean).length;
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const json = async (url) => {
  const response = await fetch(url);
  const value = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(`${response.status} ${JSON.stringify(value)}`);
  return value;
};
const waitHTTP = async (url, timeout = Math.max(30000, recordCount * 5)) => {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    try { return await json(url); } catch { await sleep(50); }
  }
  throw new Error(`timed out waiting for ${url}`);
};

const children = [];
let browser;
try {
  const app = spawn(join(args.app, "Agent_b.exe"), ["-config", join(args.data, "harness.json"), "-app-root", args.app, "-data-root", args.data, "-replay", args.replay], { windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
  children.push(app);
  app.stdout.on("data", (chunk) => process.stdout.write(chunk));
  app.stderr.on("data", (chunk) => process.stderr.write(chunk));
  const finalState = await waitHTTP(`http://127.0.0.1:${port}/api/state`);
  const finalSession = finalState.sessions?.main;
  assert.ok(finalSession, "operator main session missing from replay");

  browser = await chromium.launch({ channel: "msedge", headless: true });
  const context = await browser.newContext();
  await context.addInitScript(() => {
    const evidence = window.__agentbStreamingReplay = { renderErrors: [], remounts: [], toolKeys: [], patchEvents: 0, stateFetches: [] };
    const NativeEventSource = window.EventSource;
    window.EventSource = class extends NativeEventSource {
      constructor(...values) {
        super(...values);
        this.addEventListener("projection.patch", () => { evidence.patchEvents++; });
      }
    };
    const nativeFetch = window.fetch.bind(window);
    window.fetch = (...values) => {
      if (String(values[0]).includes("/api/state")) evidence.stateFetches.push(new Error("state fetch").stack);
      return nativeFetch(...values);
    };
    const originalError = console.error.bind(console);
    console.error = (...values) => {
      const line = values.map((value) => {
        try { return typeof value === "string" ? value : JSON.stringify(value); }
        catch { return String(value); }
      }).join(" ");
      if (line.includes("chat render failure")) evidence.renderErrors.push(line);
      originalError(...values);
    };
    const rows = new Map();
    const inspect = (root) => {
      if (!(root instanceof Element)) return;
      const candidates = root.matches("[data-entry-key]") ? [root] : [];
      candidates.push(...root.querySelectorAll("[data-entry-key]"));
      for (const row of candidates) {
        const tool = row.querySelector(".tool-tick");
        const failure = row.classList.contains("chat-render-failure");
        if (!tool && !failure) continue;
        const key = row.dataset.entryKey;
        if (!key) continue;
        const previous = rows.get(key);
        if (previous && previous !== row) evidence.remounts.push({ key, from: previous.className, to: row.className });
        rows.set(key, row);
        if (tool && !evidence.toolKeys.includes(key)) evidence.toolKeys.push(key);
      }
    };
    new MutationObserver((mutations) => {
      for (const mutation of mutations) for (const node of mutation.addedNodes) inspect(node);
    }).observe(document, { childList: true, subtree: true });
  });
  const page = await context.newPage();
  const replayTimeout = Math.max(30000, recordCount * 25);
  page.setDefaultTimeout(replayTimeout);
  await page.goto(`http://127.0.0.1:${port}/chat?session=main`, { waitUntil: "domcontentloaded" });
  const finalCursor = finalSession.cursor;
  const replayDeadline = Date.now() + replayTimeout;
  let streamedCursor;
  while (Date.now() < replayDeadline) {
    streamedCursor = await page.evaluate(async () => (await import("/static/js/bus.js")).store.sessions?.main?.cursor);
    if (streamedCursor?.generation === finalCursor.generation && Number(streamedCursor?.offset || 0) === Number(finalCursor.offset || 0)) break;
    await sleep(100);
  }
  assert.deepEqual(streamedCursor, finalCursor, `streaming replay did not reach the final cursor in ${replayTimeout} ms`);
  await page.waitForTimeout(150);
  const result = await page.evaluate(async () => {
    const { store } = await import("/static/js/bus.js");
    return {
      cursor: store.sessions?.main?.cursor,
      mountedRenderFailures: document.querySelectorAll(".chat-render-failure").length,
      ...window.__agentbStreamingReplay,
    };
  });
  assert.deepEqual(result.cursor, finalCursor, JSON.stringify(result));
  assert.equal(result.patchEvents, recordCount, `streaming replay frame count differs from tape records: ${JSON.stringify(result)}`);
  assert.deepEqual(result.stateFetches.filter((stack) => stack.includes("at resync")), [], JSON.stringify(result.stateFetches));
  assert.ok(result.toolKeys.length > 0, `streaming replay did not mount any condensed tool rows: ${JSON.stringify(result)}`);
  assert.deepEqual(result.remounts, [], JSON.stringify(result.remounts));
  assert.deepEqual(result.renderErrors, [], JSON.stringify(result.renderErrors));
  assert.equal(result.mountedRenderFailures, 0, JSON.stringify(result));
  process.stdout.write(`${JSON.stringify({
    result: "PASS streaming replay",
    tape: args.replay,
    records: recordCount,
    projectedChatEntries: finalSession.chat?.length || 0,
    condensedToolRowsObserved: result.toolKeys.length,
    condensedToolRowRemounts: result.remounts.length,
    projectionPatchesObserved: result.patchEvents,
    chatRenderFailureErrors: result.renderErrors.length,
    mountedRenderFailures: result.mountedRenderFailures,
    cursor: result.cursor,
  }, null, 2)}\n`);
} finally {
  await browser?.close().catch(() => {});
  for (const child of children.reverse()) { try { child.kill(); } catch {} }
  await sleep(250);
}
