import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const html = await readFile(new URL("../index.html", import.meta.url), "utf8");
const workspace = await readFile(new URL("./workspace.js", import.meta.url), "utf8");
const bus = await readFile(new URL("./bus.js", import.meta.url), "utf8");
const chat = await readFile(new URL("./chat.js", import.meta.url), "utf8");
const consoleApp = await readFile(new URL("./app.js", import.meta.url), "utf8");
const server = await readFile(new URL("../../internal/web/server.go", import.meta.url), "utf8");

test("Chat and Console share one served document and switch without document navigation", () => {
  assert.match(html, /id="chat-log"/);
  assert.match(html, /id="console-surface"/);
  assert.match(html, /src="\/static\/js\/workspace\.js"/);
  assert.match(server, /r\.URL\.Path == "\/chat"[\s\S]*name = "index\.html"/);
  assert.doesNotMatch(workspace, /location\.assign|location\.replace|location\.reload/);
  assert.match(workspace, /window\.history\.pushState/);
  assert.match(workspace, /window\.addEventListener\("popstate"/);
});

test("view modules preserve state while unmounted and the bus owns one stream", () => {
  assert.match(chat, /export function mountChat/);
  assert.match(chat, /export function unmountChat/);
  assert.match(consoleApp, /export function mountConsole/);
  assert.match(consoleApp, /export function unmountConsole/);
  assert.doesNotMatch(workspace, /replaceChildren|innerHTML|new EventSource/);
  assert.equal((bus.match(/new EventSource\(/g) || []).length, 1);
  assert.match(bus, /const listeners = new Set\(\)/);
});

test("same-document telemetry is explicit", () => {
  assert.match(workspace, /fullDocument: false/);
  assert.match(workspace, /mountChat\(shell\)/);
  assert.match(workspace, /mountConsole\(\)/);
});
