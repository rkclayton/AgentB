import assert from "node:assert/strict";
import test from "node:test";
import { createNavigationTelemetry } from "./navigation-telemetry.js";

function memoryStorage() {
  const values = new Map();
  return {
    getItem: (key) => values.has(key) ? values.get(key) : null,
    setItem: (key, value) => values.set(key, String(value)),
    removeItem: (key) => values.delete(key),
  };
}

function fakeRuntime(storage, origin, navigation = null) {
  let mono = 0;
  const frames = [];
  const posts = [];
  return {
    now: () => mono,
    epochNow: () => origin + mono,
    timeOrigin: () => origin,
    navigation: () => navigation,
    storage,
    fetch: async (path, options) => { posts.push({ path, options }); return { ok: true }; },
    raf: (fn) => frames.push(fn),
    onDOMContentLoaded: (fn) => fn(),
    randomID: () => "nav-1",
    advance(value) { mono = value; },
    frame(value) { mono = value; frames.shift()?.(); },
    posts,
  };
}

test("same-document settings navigation records zero absent phases and context", async () => {
  const storage = memoryStorage();
  const runtime = fakeRuntime(storage, 1000);
  runtime.advance(100);
  const telemetry = createNavigationTelemetry(runtime);
  telemetry.begin({ kind: "settings", from: "console", to: "settings", fullDocument: false, chatID: "s1" });
  runtime.advance(105);
  telemetry.surfaceReady("settings", { mutation_token: "token", active: "s1", sessions: { s1: { id: "s1", chat: [{}, {}, {}], model_unreachable: null } } });
  runtime.frame(120);
  runtime.frame(140);
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(runtime.posts.length, 1);
  const body = JSON.parse(runtime.posts[0].options.body);
  assert.equal(body.navigation_kind, "settings");
  assert.equal(body.document_request_parse_ms, 0);
  assert.equal(body.module_page_init_ms, 0);
  assert.equal(body.session_state_fetch_ms, 0);
  assert.equal(body.event_stream_connect_ms, 0);
  assert.equal(body.transcript_surface_rebuild_paint_ms, 40);
  assert.equal(body.end_to_end_ms, 40);
  assert.equal(body.transcript_entries, 3);
  assert.equal(body.chat_id, "s1");
  assert.equal(body.model_reachability, "reachable");
  assert.equal(body.since_previous_navigation_ms, null);
  assert.equal(runtime.posts[0].options.keepalive, true);
});

test("cross-document flip carries the click and records all five harness boundaries", async () => {
  const storage = memoryStorage();
  const source = fakeRuntime(storage, 1000);
  source.advance(100);
  createNavigationTelemetry(source).begin({ kind: "flip", from: "console", to: "chat", fullDocument: true, chatID: "large" });

  const target = fakeRuntime(storage, 1110, { startTime: 0, domInteractive: 20, domContentLoadedEventEnd: 25 });
  const telemetry = createNavigationTelemetry(target);
  target.advance(5); telemetry.eventSourceConstructed();
  target.advance(6); const stateFetch = telemetry.stateFetchStarted("/api/state");
  target.advance(9); telemetry.stateFetchEnded(stateFetch);
  target.advance(10); telemetry.eventSourceOpened();
  target.advance(11); telemetry.snapshotStarted();
  target.advance(12); telemetry.surfaceReady("chat", { mutation_token: "token", active: "large", sessions: { large: { id: "large", chat: new Array(400), model_unreachable: {} } } });
  target.frame(25);
  target.frame(40);
  await new Promise((resolve) => setImmediate(resolve));

  const body = JSON.parse(target.posts[0].options.body);
  assert.equal(body.document_request_parse_ms, 30);
  assert.equal(body.module_page_init_ms, 5);
  assert.equal(body.session_state_fetch_ms, 3);
  assert.equal(body.event_stream_connect_ms, 5);
  assert.equal(body.transcript_surface_rebuild_paint_ms, 29);
  assert.equal(body.end_to_end_ms, 50);
  assert.equal(body.transcript_entries, 400);
  assert.equal(body.model_reachability, "unreachable");
});
