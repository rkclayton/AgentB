import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { createServer } from "node:http";
import { chromium } from "@playwright/test";

const argv = process.argv.slice(2);
const args = Object.fromEntries(Array.from({ length: Math.floor(argv.length / 2) }, (_, index) => [argv[index * 2].replace(/^--/, ""), argv[index * 2 + 1]]));
for (const name of ["base", "evidence", "expected-commit"]) assert.ok(args[name], `missing --${name}`);

const sleep = (ms) => new Promise((done) => setTimeout(done, ms));
async function freePort() {
  const server = createServer();
  await new Promise((done) => server.listen(0, "127.0.0.1", done));
  const port = server.address().port;
  await new Promise((done) => server.close(done));
  assert.notEqual(port, 7337);
  assert.notEqual(port, 8790);
  return port;
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

const edge = [
  process.env["ProgramFiles(x86)"] && join(process.env["ProgramFiles(x86)"], "Microsoft/Edge/Application/msedge.exe"),
  process.env.ProgramFiles && join(process.env.ProgramFiles, "Microsoft/Edge/Application/msedge.exe"),
].filter(Boolean).find((candidate) => existsSync(candidate));
assert.ok(edge, "Microsoft Edge executable was not found");

const base = new URL(args.base);
assert.equal(base.hostname, "127.0.0.1");
assert.notEqual(Number(base.port), 7337);
const stateResponse = await fetch(new URL("api/state", base));
assert.equal(stateResponse.status, 200);
const state = await stateResponse.json();
assert.equal(state.build.commit, args["expected-commit"]);
assert.equal(state.build.dirty, false);

const tempRoot = await mkdtemp(join(tmpdir(), "Agent_b-lifecycle-availability-"));
const profile = join(tempRoot, "edge-profile");
const debugPort = await freePort();
const navigationID = `availability-${Date.now()}`;
const initialURL = new URL("chat?session=main", base).href;
const targetURL = new URL(`?navigation_id=${navigationID}`, base).href;
const browserArguments = [
  `--app=${initialURL}`,
  `--remote-debugging-port=${debugPort}`,
  "--remote-allow-origins=*",
  `--user-data-dir=${profile}`,
  "--no-first-run",
  "--no-default-browser-check",
  "--disable-background-mode",
];
const edgeProcess = spawn(edge, browserArguments, { windowsHide: true, stdio: "ignore" });
let browser;
try {
  const endpoint = `http://127.0.0.1:${debugPort}`;
  await waitFor(async () => (await fetch(`${endpoint}/json/version`)).ok, "remote-debug endpoint");
  browser = await chromium.connectOverCDP(endpoint);
  const page = await waitFor(() => browser.contexts().flatMap((context) => context.pages()).find((candidate) => candidate.url().startsWith(base.origin)), "app page");
  const cdp = await page.context().newCDPSession(page);
  const events = [];
  for (const name of ["frameNavigated", "domContentEventFired", "loadEventFired", "lifecycleEvent"]) {
    cdp.on(`Page.${name}`, (event) => events.push({ event: name, observed_at: new Date().toISOString(), ...event }));
  }
  await cdp.send("Page.enable");
  await cdp.send("Page.setLifecycleEventsEnabled", { enabled: true });
  const navigation = await cdp.send("Page.navigate", { url: targetURL });
  assert.ok(navigation.frameId, "Page.navigate returned no frame id");
  await waitFor(() => events.some((event) => event.event === "frameNavigated" && event.frame?.url.includes(navigationID)), "frameNavigated");
  await waitFor(() => events.some((event) => event.event === "domContentEventFired"), "domContentEventFired");
  await waitFor(() => events.some((event) => event.event === "loadEventFired"), "loadEventFired");
  await waitFor(() => events.some((event) => event.event === "lifecycleEvent" && event.name === "DOMContentLoaded"), "DOMContentLoaded lifecycle");
  await waitFor(() => events.some((event) => event.event === "lifecycleEvent" && event.name === "load"), "load lifecycle");
  const output = {
    schema: 1,
    measured_at: new Date().toISOString(),
    build: state.build,
    attachment: { method: "connectOverCDP + newCDPSession(page)", browser_arguments: browserArguments.map((value) => value.replace(profile, "<isolated-temp-profile>")) },
    navigation: { navigation_id: navigationID, target_url: targetURL, frame_id: navigation.frameId, loader_id: navigation.loaderId || "" },
    available: {
      frame_navigated: events.some((event) => event.event === "frameNavigated"),
      dom_content_event_fired: events.some((event) => event.event === "domContentEventFired"),
      load_event_fired: events.some((event) => event.event === "loadEventFired"),
      lifecycle_dom_content_loaded: events.some((event) => event.event === "lifecycleEvent" && event.name === "DOMContentLoaded"),
      lifecycle_load: events.some((event) => event.event === "lifecycleEvent" && event.name === "load"),
    },
    events,
  };
  const evidence = resolve(args.evidence);
  await mkdir(evidence, { recursive: false });
  await writeFile(join(evidence, "availability.json"), JSON.stringify(output, null, 2));
  process.stdout.write(`${JSON.stringify({ build: output.build, attachment: output.attachment, navigation: output.navigation, available: output.available }, null, 2)}\n`);
} finally {
  await browser?.close().catch(() => {});
  if (edgeProcess.exitCode === null) {
    edgeProcess.kill();
    await Promise.race([new Promise((done) => edgeProcess.once("exit", done)), sleep(3000)]);
  }
  await rm(tempRoot, { recursive: true, force: true });
}
