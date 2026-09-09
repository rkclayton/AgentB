import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { join } from "node:path";
import { chromium } from "@playwright/test";

const args = Object.fromEntries(Array.from({ length: Math.floor(process.argv.slice(2).length / 2) }, (_, index) => {
  const offset = index * 2 + 2;
  return [process.argv[offset].replace(/^--/, ""), process.argv[offset + 1]];
}));
for (const key of ["app", "data", "replay", "evidence"]) assert.ok(args[key], `missing --${key}`);

const probe = createServer();
await new Promise((resolve) => probe.listen(0, "127.0.0.1", resolve));
const freePort = probe.address().port;
await new Promise((resolve) => probe.close(resolve));
const configPath = join(args.data, "harness.json");
const config = JSON.parse(await readFile(configPath, "utf8"));
config.listen = `127.0.0.1:${freePort}`;
await writeFile(configPath, `${JSON.stringify(config, null, 2)}\n`);
const port = Number(String(config.listen).split(":").at(-1));
const baseURL = `http://127.0.0.1:${port}`;
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const children = [];
let browser;
let context;

async function waitHTTP(url, timeout = 180000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url);
      if (response.ok) return response.json();
    } catch {}
    await sleep(50);
  }
  throw new Error(`timed out waiting for ${url}`);
}

try {
  await mkdir(args.evidence, { recursive: true });
  const app = spawn(join(args.app, "Agent_b.exe"), [
    "-config", join(args.data, "harness.json"),
    "-app-root", args.app,
    "-data-root", args.data,
    "-replay", args.replay,
  ], { windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
  children.push(app);
  app.stdout.on("data", (chunk) => process.stdout.write(chunk));
  app.stderr.on("data", (chunk) => process.stderr.write(chunk));
  const state = await waitHTTP(`${baseURL}/api/state`);
  const session = state.sessions?.main;
  assert.ok(session, "production replay must contain session main");

  browser = await chromium.launch({
    channel: "msedge",
    headless: false,
    args: ["--window-size=1250,975"],
  });
  context = await browser.newContext({ viewport: { width: 1250, height: 975 } });
  const page = await context.newPage();
  page.setDefaultTimeout(120000);
  await page.goto(`${baseURL}/chat?session=main&instant=1`, { waitUntil: "domcontentloaded", timeout: 120000 });
  const tab = page.locator('.agent-tab-wrap[data-agent="agent_b"] .agent-tab');
  await tab.waitFor({ state: "visible" });
  await tab.click({ button: "right" });
  const menu = page.locator('.agent-tab-wrap[data-agent="agent_b"] .agent-chat-menu');
  await menu.waitFor({ state: "attached" });
  const geometry = await menu.evaluate((element) => {
    const rect = element.getBoundingClientRect();
    const ancestors = [];
    for (let current = element.parentElement; current; current = current.parentElement) {
      const style = getComputedStyle(current);
      if ([style.overflow, style.overflowX, style.overflowY].some((value) => ["hidden", "clip"].includes(value))) {
        const box = current.getBoundingClientRect();
        ancestors.push({ className: current.className, overflow: style.overflow, x: box.x, y: box.y, width: box.width, height: box.height });
      }
    }
    const x = Math.max(0, Math.min(innerWidth - 1, rect.left + Math.min(8, rect.width / 2)));
    const y = Math.max(0, Math.min(innerHeight - 1, rect.top + Math.min(8, rect.height / 2)));
    const hit = document.elementFromPoint(x, y);
    return {
      hidden: element.hidden,
      menu: { x: rect.x, y: rect.y, width: rect.width, height: rect.height },
      clippingAncestors: ancestors,
      hitClass: hit?.className || "",
      menuOwnsHit: !!hit && element.contains(hit),
    };
  });
  await page.screenshot({ path: join(args.evidence, "production-tape-menu.png") });

  let rowClick = "no enabled Open row";
  const open = page.locator(".agent-chat-open:not(:disabled)").first();
  if (await open.count()) {
    try {
      await open.click({ timeout: 2000 });
      rowClick = "clicked";
    } catch (error) {
      rowClick = String(error.message).split("\n")[0];
    }
  }
  await page.screenshot({ path: join(args.evidence, "production-tape-after-click.png") });
  const reproduced = !geometry.hidden && (!geometry.menuOwnsHit || rowClick !== "clicked");
  const result = {
    tape: args.replay,
    projectedChatEntries: session.chat?.length || 0,
    viewport: { width: 1250, height: 975 },
    geometry,
    rowClick,
    reproduced,
  };
  process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
  const expected = args.expected || "reproduced";
  assert.ok(["reproduced", "fixed"].includes(expected), `invalid --expected ${expected}`);
  assert.equal(reproduced, expected === "reproduced", `production-tape menu was not ${expected}`);
} finally {
  await context?.close().catch(() => {});
  await browser?.close().catch(() => {});
  for (const child of children.reverse()) child.kill();
  await sleep(250);
}
