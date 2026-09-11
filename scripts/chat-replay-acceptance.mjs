import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { chromium } from "@playwright/test";

const args = Object.fromEntries(Array.from({ length: Math.floor(process.argv.slice(2).length / 2) }, (_, index) => {
  const offset = index * 2 + 2;
  return [process.argv[offset].replace(/^--/, ""), process.argv[offset + 1]];
}));
for (const key of ["app", "data", "replay", "evidence"]) assert.ok(args[key], `missing --${key}`);
await mkdir(dirname(args.evidence), { recursive: true });
await mkdir(args.evidence);
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
const sessionID = args.session || "main";
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const json = async (url) => {
  const response = await fetch(url);
  const value = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(`${response.status} ${JSON.stringify(value)}`);
  return value;
};
const waitHTTP = async (url, timeout = Math.max(30000, recordCount * 25)) => {
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
  const finalSession = finalState.sessions?.[sessionID];
  assert.ok(finalSession, `session ${sessionID} missing from replay`);

  browser = await chromium.launch({ channel: "msedge", headless: true });
  const context = await browser.newContext({ viewport: { width: 1250, height: 975 } });
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
  const pageErrors = [];
  const consoleErrors = [];
  const failedResponses = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("console", (message) => { if (message.type() === "error") consoleErrors.push(message.text()); });
  page.on("response", (response) => { if (response.status() >= 400) failedResponses.push({ status: response.status(), url: response.url() }); });
  const replayTimeout = Math.max(30000, recordCount * 25);
  page.setDefaultTimeout(replayTimeout);
  await page.goto(`http://127.0.0.1:${port}/chat?session=${encodeURIComponent(sessionID)}`, { waitUntil: "domcontentloaded" });
  const finalCursor = finalSession.cursor;
  const replayDeadline = Date.now() + replayTimeout;
  let streamedCursor;
  while (Date.now() < replayDeadline) {
    streamedCursor = await page.evaluate(async (id) => (await import("/static/js/bus.js")).store.sessions?.[id]?.cursor, sessionID);
    if (streamedCursor?.generation === finalCursor.generation && Number(streamedCursor?.offset || 0) === Number(finalCursor.offset || 0)) break;
    await sleep(100);
  }
  assert.deepEqual(streamedCursor, finalCursor, `streaming replay did not reach the final cursor in ${replayTimeout} ms`);
  await page.waitForTimeout(150);
  const result = await page.evaluate(async (id) => {
    const { store } = await import("/static/js/bus.js");
    return {
      cursor: store.sessions?.[id]?.cursor,
      mountedRenderFailures: document.querySelectorAll(".chat-render-failure").length,
      ...window.__agentbStreamingReplay,
    };
  }, sessionID);
  assert.deepEqual(result.cursor, finalCursor, JSON.stringify(result));
  assert.equal(result.patchEvents, recordCount, `streaming replay frame count differs from tape records: ${JSON.stringify(result)}`);
  assert.deepEqual(result.stateFetches.filter((stack) => stack.includes("at resync")), [], JSON.stringify(result.stateFetches));
  assert.ok(result.toolKeys.length > 0, `streaming replay did not mount any condensed tool rows: ${JSON.stringify(result)}`);
  assert.deepEqual(result.remounts, [], JSON.stringify(result.remounts));
  assert.deepEqual(result.renderErrors, [], JSON.stringify(result.renderErrors));
  assert.equal(result.mountedRenderFailures, 0, JSON.stringify(result));
  const ui = await page.evaluate(() => {
    const prose = [...document.querySelectorAll(".chat-response-prose")];
    const folds = [...document.querySelectorAll(".chat-step-summary")];
    const decided = [...document.querySelectorAll(".approval-decided")];
    const alarmSummaries = [
      ...document.querySelectorAll(".chat-response.alarm > .chat-response-content > .chat-response-summary"),
      ...document.querySelectorAll(".chat-step-fold.alarm > .chat-step-summary"),
      ...document.querySelectorAll(".chat-tool-group.alarm > .chat-tool-group-head"),
    ];
    const tab = document.querySelector('.agent-tab[data-agent="agent_b"]');
    const composer = document.querySelector(".chat-composer");
    const inputWrap = document.querySelector(".chat-input-wrap");
    const attach = document.querySelector("#chat-attach")?.getBoundingClientRect();
    const stop = document.querySelector("#chat-stop")?.getBoundingClientRect();
    const send = document.querySelector("#chat-send")?.getBoundingClientRect();
    const connectionRows = [...document.querySelectorAll(".chat-notice-row")].filter((node) => node.innerText.startsWith("model unreachable ·"));
    return {
      prose: prose.length,
      visibleProse: prose.filter((node) => node.getClientRects().length > 0).length,
      folds: folds.length,
      openFolds: folds.filter((node) => node.getAttribute("aria-expanded") === "true").length,
      decided: decided.length,
      tallDecisions: decided.filter((node) => node.getBoundingClientRect().height > 21).length,
      pendingResolvedCards: [...document.querySelectorAll(".approval-card")].filter((node) => /allowed for this chat|allowed once|denied/i.test(node.innerText)).length,
      alarmsWithoutFailure: alarmSummaries.filter((node) => !/failed/.test(node.innerText)).map((node) => node.innerText),
      headerChatConsoleLinks: document.querySelectorAll('.shell-page[data-page="chat"],.shell-page[data-page="console"]').length,
      side: tab?.dataset.side,
      offline: tab?.querySelector(".agent-tab-robot")?.classList.contains("offline") || tab?.querySelector(".agent-state")?.classList.contains("offline") || false,
      replayComposerDisabled: document.querySelector("#chat-task")?.disabled && document.querySelector("#chat-send")?.disabled,
      pageOverflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      logOverflow: document.querySelector("#chat-log")?.scrollWidth - document.querySelector("#chat-log")?.clientWidth,
      agentSpeakerImages: document.querySelectorAll(".chat-agent .chat-speaker img").length,
      userSpeakerImages: document.querySelectorAll(".chat-user .chat-speaker img").length,
      connectionRows: connectionRows.length,
      nestedConnectionRows: [...document.querySelectorAll(".chat-response-notice")].filter((node) => node.innerText.startsWith("model unreachable ·")).length,
      composerLeftInset: Math.round(inputWrap.getBoundingClientRect().left - composer.getBoundingClientRect().left),
      composerRightInset: Math.round(composer.getBoundingClientRect().right - inputWrap.getBoundingClientRect().right),
      actionSizes: [attach, stop, send].map((rect) => ({ width: Math.round(rect.width), height: Math.round(rect.height) })),
      attachmentAboveSubmit: attach.bottom <= Math.min(stop.top, send.top),
      textareaRightPadding: getComputedStyle(document.querySelector("#chat-task")).paddingRight,
      composerOperatorControl: Boolean(document.querySelector("#chat-run-as-you")),
    };
  });
  assert.ok(ui.prose > 0, JSON.stringify(ui));
  assert.equal(ui.visibleProse, ui.prose, JSON.stringify(ui));
  assert.ok(ui.folds > 0, JSON.stringify(ui));
  assert.equal(ui.openFolds, 0, JSON.stringify(ui));
  assert.equal(ui.tallDecisions, 0, JSON.stringify(ui));
  assert.equal(ui.pendingResolvedCards, 0, JSON.stringify(ui));
  assert.deepEqual(ui.alarmsWithoutFailure, [], JSON.stringify(ui));
  assert.equal(ui.headerChatConsoleLinks, 0, JSON.stringify(ui));
  assert.equal(ui.side, "chat", JSON.stringify(ui));
  assert.equal(ui.offline, Boolean(finalSession.model_unreachable), JSON.stringify(ui));
  assert.equal(ui.replayComposerDisabled, true, JSON.stringify(ui));
  assert.ok(ui.pageOverflow <= 0 && ui.logOverflow <= 0, JSON.stringify(ui));
  assert.ok(ui.agentSpeakerImages > 0, JSON.stringify(ui));
  assert.equal(ui.userSpeakerImages, 0, JSON.stringify(ui));
  assert.ok(ui.connectionRows > 0, JSON.stringify(ui));
  assert.equal(ui.nestedConnectionRows, 0, JSON.stringify(ui));
  assert.deepEqual(ui.actionSizes, [{ width: 24, height: 24 }, { width: 24, height: 24 }, { width: 24, height: 24 }], JSON.stringify(ui));
  assert.equal(ui.attachmentAboveSubmit, true, JSON.stringify(ui));
  assert.equal(ui.textareaRightPadding, "64px", JSON.stringify(ui));
  assert.equal(ui.composerOperatorControl, false, JSON.stringify(ui));
  assert.equal(ui.composerLeftInset, 8, JSON.stringify(ui));
  assert.equal(ui.composerRightInset, 8, JSON.stringify(ui));
  const shellGeometry = () => page.evaluate(() => Object.fromEntries([
    ["shell", "#app-shell"],
    ["tabs", ".agent-tabs"],
    ["wrap", '.agent-tab-wrap[data-agent="agent_b"]'],
    ["tab", '.agent-tab[data-agent="agent_b"]'],
    ["plus", ".agent-tab-new"],
    ["plan", ".shell-page"],
    ["settings", ".shell-settings"],
  ].map(([key, selector]) => {
    const rect = document.querySelector(selector).getBoundingClientRect();
    return [key, { x: rect.x, y: rect.y, width: rect.width, height: rect.height }];
  })));
  const chatGeometry = await shellGeometry();
  await page.locator(".chat-notice-row").filter({ hasText: "model unreachable ·" }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(args.evidence, "real-tape-chat.png") });

  const firstFold = page.locator(".chat-step-summary").first();
  const proseBefore = await page.locator(".chat-response-prose").allTextContents();
  await firstFold.click();
  assert.equal(await firstFold.getAttribute("aria-expanded"), "true");
  assert.deepEqual(await page.locator(".chat-response-prose").allTextContents(), proseBefore);
  await firstFold.click();

  const tab = page.locator('.agent-tab[data-agent="agent_b"]');
  await tab.click({ button: "right" });
  const historyRows = page.locator('.agent-tab-wrap[data-agent="agent_b"] .agent-chat-row');
  assert.equal(await historyRows.count(), Object.keys(finalState.sessions || {}).length);
  await page.screenshot({ path: join(args.evidence, "real-tape-menu.png") });
  await page.keyboard.press("Escape");
  await tab.click();
  await page.waitForURL((url) => url.pathname === "/" && url.searchParams.get("session") === sessionID);
  await page.locator('.agent-tab[data-agent="agent_b"]').waitFor();
  await page.locator("#console-agent option", { hasText: "Acceptance" }).waitFor({ state: "attached" });
  await page.locator("#flow .activity-row").first().waitFor();
  assert.equal(await page.locator('.agent-tab[data-agent="agent_b"]').getAttribute("data-side"), "console");
  const consoleGeometry = await shellGeometry();
  const consoleCapabilities = await page.evaluate(() => {
    const visible = (selector) => {
      const node = document.querySelector(selector);
      if (!node) return false;
      const style = getComputedStyle(node);
      const box = node.getBoundingClientRect();
      return !node.hidden && style.display !== "none" && style.visibility !== "hidden" && box.width > 0 && box.height > 0;
    };
    return {
      groups: [...document.querySelectorAll(".console-surface > .console-group > .panel-caption > span:first-child")].map((node) => node.textContent.trim()),
      visible: Object.fromEntries(["#console-agent", "#console-agent-binding", "#console-tools-panel", "#console-lifetime", "#console-live-content", "#rail", "#flow", ".rack-well", "#state-list", "#timeline-list", "#clear-stats", "#flush-memory"].map((selector) => [selector, visible(selector)])),
      pageOverflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      fixedHeaderHeight: document.querySelector("#app-shell").getBoundingClientRect().height,
      selectedAgent: document.querySelector("#console-agent")?.selectedOptions[0]?.textContent || "",
      flowRows: document.querySelectorAll("#flow .activity-row").length,
    };
  });
  assert.deepEqual(consoleCapabilities.groups, ["Agent", "Tools", "Lifetime", "Live run", "Maintenance"], JSON.stringify(consoleCapabilities));
  assert.ok(Object.values(consoleCapabilities.visible).every(Boolean), JSON.stringify(consoleCapabilities));
  assert.ok(consoleCapabilities.pageOverflow <= 0, JSON.stringify(consoleCapabilities));
  assert.equal(consoleCapabilities.fixedHeaderHeight, chatGeometry.shell.height, JSON.stringify(consoleCapabilities));
  assert.equal(consoleCapabilities.selectedAgent, "Acceptance", JSON.stringify(consoleCapabilities));
  assert.ok(consoleCapabilities.flowRows > 0, JSON.stringify(consoleCapabilities));
  await page.screenshot({ path: join(args.evidence, "real-tape-console.png") });
  const consoleWidths = [];
  for (const width of [820, 520, 320]) {
    await page.setViewportSize({ width, height: 975 });
    const layout = await page.evaluate(() => ({
      width: document.documentElement.clientWidth,
      overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      groups: [...document.querySelectorAll(".console-surface > .console-group > .panel-caption > span:first-child")].filter((node) => getComputedStyle(node).display !== "none").map((node) => node.textContent.trim()),
      stateDisplay: getComputedStyle(document.querySelector(".state-well")).display,
      historyDisplay: getComputedStyle(document.querySelector(".timeline-well")).display,
      bindingVisible: document.querySelector("#console-agent-binding").getClientRects().length > 0,
      historyOverflow: document.querySelector(".timeline-well").scrollWidth - document.querySelector(".timeline-well").clientWidth,
      timelineOverlaps: [...document.querySelectorAll(".timeline-head")].reduce((count, head) => {
        const boxes = [...head.children].map((node) => node.getBoundingClientRect()).filter((box) => box.width > 0 && box.height > 0);
        return count + boxes.reduce((rowCount, box, index) => rowCount + boxes.slice(index + 1).filter((other) => box.left < other.right && box.right > other.left && box.top < other.bottom && box.bottom > other.top).length, 0);
      }, 0),
    }));
    assert.equal(layout.overflow, 0, JSON.stringify(layout));
    assert.deepEqual(layout.groups, consoleCapabilities.groups, JSON.stringify(layout));
    assert.notEqual(layout.stateDisplay, "none", JSON.stringify(layout));
    assert.notEqual(layout.historyDisplay, "none", JSON.stringify(layout));
    assert.equal(layout.bindingVisible, true, JSON.stringify(layout));
    assert.ok(layout.historyOverflow <= 0, JSON.stringify(layout));
    assert.equal(layout.timelineOverlaps, 0, JSON.stringify(layout));
    await page.screenshot({ path: join(args.evidence, `real-tape-console-${width}.png`) });
    const bottom = await page.evaluate(() => {
      const surface = document.querySelector("#console-surface");
      surface.scrollTop = surface.scrollHeight;
      const box = document.querySelector(".console-maintenance").getBoundingClientRect();
      return { scrollTop: surface.scrollTop, scrollHeight: surface.scrollHeight, maintenanceTop: box.top, maintenanceBottom: box.bottom, maintenanceReachable: box.top >= 32 && box.bottom <= innerHeight };
    });
    assert.equal(bottom.maintenanceReachable, true, JSON.stringify(bottom));
    consoleWidths.push({ ...layout, ...bottom });
    await page.screenshot({ path: join(args.evidence, `real-tape-console-${width}-bottom.png`) });
    await page.locator("#console-surface").evaluate((surface) => { surface.scrollTop = 0; });
  }
  await page.setViewportSize({ width: 1250, height: 975 });
  await page.locator('.agent-tab[data-agent="agent_b"]').click();
  await page.waitForURL((url) => url.pathname === "/chat" && url.searchParams.get("session") === sessionID);
  await page.locator(".chat-entry").first().waitFor();
  const returnedChatGeometry = await shellGeometry();
  if (args["expect-stable-shell"] === "true") {
    assert.deepEqual(consoleGeometry, chatGeometry, JSON.stringify({ chatGeometry, consoleGeometry }));
    assert.deepEqual(returnedChatGeometry, chatGeometry, JSON.stringify({ chatGeometry, returnedChatGeometry }));
  }
  assert.deepEqual(pageErrors, []);
  const chatFailedResponses = [...failedResponses];
  assert.ok(chatFailedResponses.every((response) => response.status === 404 && response.url.includes("/api/files/")), JSON.stringify(chatFailedResponses));
  const chatConsoleErrors = [...consoleErrors];
  assert.equal(chatConsoleErrors.length, chatFailedResponses.length, JSON.stringify({ chatConsoleErrors, chatFailedResponses }));
  consoleErrors.length = 0;
  await page.goto(`http://127.0.0.1:${port}/?session=${encodeURIComponent(sessionID)}#settings/shell`, { waitUntil: "domcontentloaded" });
  const operatorToggle = page.locator('.settings-operator-status[data-action="operator-context"]');
  await operatorToggle.waitFor();
  assert.match(await operatorToggle.innerText(), /Run everything as me|Stop running everything as me/);
  await page.screenshot({ path: join(args.evidence, "real-tape-settings.png") });
  assert.deepEqual(pageErrors, []);
  const settingsConsoleErrors = [...consoleErrors];
  const settingsFailedResponses = failedResponses.slice(chatFailedResponses.length);
  assert.ok(settingsConsoleErrors.every((message) => message.startsWith("Failed to load resource: the server responded with a status of ")), JSON.stringify(settingsConsoleErrors));
  const report = {
    result: "PASS streaming replay",
    tape: args.replay,
    records: recordCount,
    projectedChatEntries: finalSession.chat?.length || 0,
    condensedToolRowsObserved: result.toolKeys.length,
    condensedToolRowRemounts: result.remounts.length,
    projectionPatchesObserved: result.patchEvents,
    chatRenderFailureErrors: result.renderErrors.length,
    mountedRenderFailures: result.mountedRenderFailures,
    pageErrors,
    consoleErrors: chatConsoleErrors,
    settingsConsoleErrors,
    settingsFailedResponses,
    chatFailedResponses,
    ui,
    consoleCapabilities,
    consoleWidths,
    shellGeometry: { chat: chatGeometry, console: consoleGeometry, returned_chat: returnedChatGeometry },
    plusUsableHitTarget: chatGeometry.plus.width >= 20 && chatGeometry.plus.height >= 20,
    cursor: result.cursor,
  };
  await writeFile(join(args.evidence, "result.json"), `${JSON.stringify(report, null, 2)}\n`);
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
} finally {
  await browser?.close().catch(() => {});
  for (const child of children.reverse()) { try { child.kill(); } catch {} }
  await sleep(250);
}
