#!/usr/bin/env node

import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { existsSync } from "node:fs";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { chromium } from "playwright";

const argv = process.argv.slice(2);
const args = Object.fromEntries(Array.from({ length: Math.floor(argv.length / 2) }, (_, index) => [argv[index * 2].replace(/^--/, ""), argv[index * 2 + 1]]));
assert.ok(args.evidence, "missing --evidence");

const edge = [
  process.env["ProgramFiles(x86)"] && join(process.env["ProgramFiles(x86)"], "Microsoft", "Edge", "Application", "msedge.exe"),
  process.env.ProgramFiles && join(process.env.ProgramFiles, "Microsoft", "Edge", "Application", "msedge.exe"),
].find((candidate) => candidate && existsSync(candidate));
assert.ok(edge, "Microsoft Edge was not found");

const sleep = (milliseconds) => new Promise((done) => setTimeout(done, milliseconds));
async function freePort() {
  const probe = createServer();
  await new Promise((done) => probe.listen(0, "127.0.0.1", done));
  const port = probe.address().port;
  await new Promise((done) => probe.close(done));
  assert.notEqual(port, 7337);
  assert.notEqual(port, 8790);
  return port;
}

const title = `Agent_b WCO Probe ${Date.now()}`;
const httpPort = await freePort();
const debugPort = await freePort();
const profile = join(tmpdir(), `Agent_b-wco-${process.pid}-${Date.now()}`);
const manifest = {
  name: "Agent_b WCO Probe",
  short_name: "WCO Probe",
  start_url: "/",
  scope: "/",
  display: "standalone",
  display_override: ["window-controls-overlay"],
};
const html = `<!doctype html><html><head><meta charset="utf-8"><title>${title}</title><link rel="manifest" href="/manifest.json"><style>:root{--app-header-height:32px}.window-titlebar{height:var(--app-header-height);width:env(titlebar-area-width,100%);-webkit-app-region:drag;background:#2A2E35;color:#D8DDE3}</style></head><body style="margin:0"><header class="window-titlebar">Agent_b WCO probe</header><main>probe</main></body></html>`;
const server = createServer((request, response) => {
  if (request.url === "/manifest.json") {
    response.writeHead(200, { "Content-Type": "application/manifest+json" });
    response.end(JSON.stringify(manifest));
  } else {
    response.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
    response.end(html);
  }
});

let edgeProcess;
let browser;
try {
  await mkdir(resolve(args.evidence), { recursive: false });
  await new Promise((done) => server.listen(httpPort, "127.0.0.1", done));
  edgeProcess = spawn(edge, [
    `--app=http://127.0.0.1:${httpPort}/`,
    `--remote-debugging-port=${debugPort}`,
    "--remote-allow-origins=*",
    `--user-data-dir=${profile}`,
    "--no-first-run",
    "--no-default-browser-check",
    "--disable-background-mode",
    "--window-size=1250,975",
  ], { windowsHide: false, stdio: "ignore" });
  const endpoint = `http://127.0.0.1:${debugPort}`;
  const deadline = Date.now() + 15000;
  while (!browser && Date.now() < deadline) {
    try { browser = await chromium.connectOverCDP(endpoint); } catch { await sleep(50); }
  }
  assert.ok(browser, "Edge remote-debug endpoint did not become ready");
  const pages = browser.contexts().flatMap((context) => context.pages());
  const page = pages.find((candidate) => candidate.url().startsWith(`http://127.0.0.1:${httpPort}/`));
  assert.ok(page, "probe page was not found");
  await page.waitForLoadState("load");
  const web = await page.evaluate(() => ({
    user_agent: navigator.userAgent,
    secure_context: window.isSecureContext,
    overlay_api_available: "windowControlsOverlay" in navigator,
    overlay_visible: navigator.windowControlsOverlay?.visible ?? null,
    overlay_rect: navigator.windowControlsOverlay ? { ...navigator.windowControlsOverlay.getTitlebarAreaRect().toJSON() } : null,
    overlay_display_mode: matchMedia("(display-mode: window-controls-overlay)").matches,
    standalone_display_mode: matchMedia("(display-mode: standalone)").matches,
    app_header_height_px: document.querySelector(".window-titlebar").getBoundingClientRect().height,
    outer_height_px: outerHeight,
    inner_height_px: innerHeight,
    outer_minus_inner_height_px: outerHeight - innerHeight,
  }));
  await page.screenshot({ path: resolve(args.evidence, "app-mode.png") });
  const output = {
    schema: 1,
    measured_at: new Date().toISOString(),
    edge,
    launch: { mode: "--app=<loopback-url>", installed_pwa: false, isolated_profile: true },
    manifest,
    web,
    system_strip: { height_px: web.outer_minus_inner_height_px, method: "window.outerHeight - window.innerHeight in app mode; app mode has no navigation/status rows below the document" },
    conclusion: web.overlay_visible || web.overlay_display_mode ? "window-controls-overlay active in --app mode" : "window-controls-overlay inactive in --app mode",
    platform_contract: "Microsoft Edge documents Window Controls Overlay for an installed desktop PWA with manifest display_override window-controls-overlay.",
  };
  await writeFile(resolve(args.evidence, "result.json"), `${JSON.stringify(output, null, 2)}\n`);
  process.stdout.write(`${JSON.stringify(output, null, 2)}\n`);
} finally {
  try { await browser?.close(); } catch {}
  if (edgeProcess && edgeProcess.exitCode === null) {
    try { edgeProcess.kill(); } catch {}
    await Promise.race([new Promise((done) => edgeProcess.once("exit", done)), sleep(3000)]);
  }
  server.closeAllConnections?.();
  await new Promise((done) => server.close(done)).catch(() => {});
  if (profile.startsWith(join(tmpdir(), "Agent_b-wco-"))) {
    try { await rm(profile, { recursive: true, force: true }); }
    catch (error) { process.stderr.write(`cleanup warning: ${error.message}\n`); }
  }
}
