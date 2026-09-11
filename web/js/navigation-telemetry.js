const pendingKey = "agentb.navigation.pending.v1";
const lastKey = "agentb.navigation.last-start.v1";

function round(value) {
  return Math.round(Math.max(0, Number(value) || 0) * 1000) / 1000;
}

function browserRuntime() {
  if (typeof window === "undefined") return null;
  return {
    now: () => performance.now(),
    epochNow: () => performance.timeOrigin + performance.now(),
    timeOrigin: () => performance.timeOrigin,
    navigation: () => performance.getEntriesByType("navigation")[0] || null,
    storage: globalThis.sessionStorage,
    fetch: (...args) => globalThis.fetch(...args),
    raf: (fn) => requestAnimationFrame(fn),
    onDOMContentLoaded: (fn) => document.addEventListener("DOMContentLoaded", fn, { once: true }),
    randomID: () => globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`,
  };
}

export function createNavigationTelemetry(runtime) {
  let active = readPending();
  let postPromise = null;

  function measured(fn) {
    if (!active || active.state === "complete") return fn();
    const started = runtime.now();
    try { return fn(); }
    finally { active.instrumentation_sync_ms = (active.instrumentation_sync_ms || 0) + Math.max(0, runtime.now() - started); }
  }

  function readPending() {
    try {
      const value = JSON.parse(runtime.storage?.getItem(pendingKey) || "null");
      return value && typeof value.navigation_id === "string" ? value : null;
    } catch { return null; }
  }

  function persist(value) {
    try { runtime.storage?.setItem(pendingKey, JSON.stringify(value)); } catch {}
  }

  function begin({ kind, from, to, fullDocument, chatID = "" }) {
    const instrumentationStarted = runtime.now();
    const clickedAt = runtime.epochNow();
    let previous = null;
    try {
      const value = Number(runtime.storage?.getItem(lastKey));
      if (Number.isFinite(value) && value > 0 && value <= clickedAt) previous = value;
      runtime.storage?.setItem(lastKey, String(clickedAt));
    } catch {}
    active = {
      state: "measuring",
      navigation_id: runtime.randomID(),
      navigation_kind: kind,
      from,
      to,
      full_document: !!fullDocument,
      chat_id: chatID,
      clicked_at: clickedAt,
      since_previous_navigation_ms: previous === null ? null : round(clickedAt - previous),
      instrumentation_sync_ms: 0,
      state_fetches: [],
      event_source_constructed_at: null,
      event_source_open_at: null,
      snapshot_at: null,
      finish_at: null,
    };
    persist(active);
    active.instrumentation_sync_ms = Math.max(0, runtime.now() - instrumentationStarted);
    persist(active);
    return active.navigation_id;
  }

  function stateFetchStarted(path) {
    if (!active || active.state === "complete" || String(path).split("?", 1)[0] !== "/api/state") return null;
    return measured(() => {
      const sample = { start: runtime.epochNow(), end: null };
      active.state_fetches.push(sample);
      return sample;
    });
  }

  function stateFetchEnded(sample) {
    if (!sample || !active || active.state === "complete") return;
    measured(() => { sample.end = runtime.epochNow(); });
    tryFinalize();
  }

  function eventSourceConstructed() {
    if (!active || active.state === "complete" || active.event_source_constructed_at !== null) return;
    measured(() => { active.event_source_constructed_at = runtime.epochNow(); });
  }

  function eventSourceOpened() {
    if (!active || active.state === "complete" || active.event_source_open_at !== null) return;
    measured(() => { active.event_source_open_at = runtime.epochNow(); });
    tryFinalize();
  }

  function snapshotStarted() {
    if (!active || active.state === "complete" || active.snapshot_at !== null) return;
    measured(() => { active.snapshot_at = runtime.epochNow(); });
  }

  function surfaceReady(surface, state) {
    if (!active) return;
    if (active.state === "complete") {
      void post(state?.mutation_token || "");
      return;
    }
    if (active.to !== surface || active.finish_at !== null) return;
    if (active.full_document && active.snapshot_at === null) return;
    const session = state?.sessions?.[active.chat_id] || state?.sessions?.[state?.active] || null;
    active.context = {
      transcript_entries: Array.isArray(session?.chat) ? session.chat.length : 0,
      chat_id: session?.id || active.chat_id || "",
      model_reachability: session ? (session.model_unreachable ? "unreachable" : "reachable") : "unknown",
      mutation_token: state?.mutation_token || "",
    };
    runtime.raf(() => runtime.raf(() => {
      if (!active || active.state === "complete" || active.to !== surface) return;
      measured(() => { active.finish_at = runtime.epochNow(); });
      tryFinalize();
    }));
  }

  function tryFinalize() {
    if (!active || active.state === "complete" || active.finish_at === null) return;
    if (active.full_document) {
      const navigation = runtime.navigation();
      if (!navigation || !navigation.domInteractive || !navigation.domContentLoadedEventEnd) {
        runtime.onDOMContentLoaded(() => tryFinalize());
        return;
      }
      if (active.event_source_constructed_at === null || active.event_source_open_at === null || active.snapshot_at === null) return;
    }
    if (active.state_fetches.some((sample) => sample.start <= active.finish_at && sample.end === null)) return;
    finalize();
  }

  function finalize() {
    const instrumentationStarted = runtime.now();
    const navigation = active.full_document ? runtime.navigation() : null;
    const fetches = active.state_fetches.filter((sample) => sample.start <= active.finish_at && sample.end !== null);
    const firstFetch = fetches.length ? Math.min(...fetches.map((sample) => sample.start)) : 0;
    const lastFetch = fetches.length ? Math.max(...fetches.map((sample) => sample.end)) : 0;
    const data = {
      navigation_id: active.navigation_id,
      navigation_kind: active.navigation_kind,
      from: active.from,
      to: active.to,
      document_request_parse_ms: navigation ? round(runtime.timeOrigin() + navigation.domInteractive - active.clicked_at) : 0,
      module_page_init_ms: navigation ? round(navigation.domContentLoadedEventEnd - navigation.domInteractive) : 0,
      session_state_fetch_ms: fetches.length ? round(lastFetch - firstFetch) : 0,
      event_stream_connect_ms: active.event_source_constructed_at !== null && active.event_source_open_at !== null ? round(active.event_source_open_at - active.event_source_constructed_at) : 0,
      transcript_surface_rebuild_paint_ms: round(active.finish_at - (active.full_document ? active.snapshot_at : active.clicked_at)),
      end_to_end_ms: round(active.finish_at - active.clicked_at),
      transcript_entries: active.context.transcript_entries,
      chat_id: active.context.chat_id,
      model_reachability: active.context.model_reachability,
      since_previous_navigation_ms: active.since_previous_navigation_ms,
      instrumentation_sync_ms: 0,
    };
    data.instrumentation_sync_ms = round(active.instrumentation_sync_ms + Math.max(0, runtime.now() - instrumentationStarted));
    const token = active.context.mutation_token;
    active = { state: "complete", navigation_id: data.navigation_id, body: data };
    persist(active);
    void post(token);
  }

  async function post(token) {
    if (!active || active.state !== "complete" || postPromise || !token) return postPromise;
    const currentID = active.navigation_id;
    postPromise = runtime.fetch("/api/navigation-measurements", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-AgentB-Mutation-Token": token },
      body: JSON.stringify(active.body),
      keepalive: true,
    }).then((response) => {
      if (!response.ok) throw new Error(`navigation measurement: HTTP ${response.status}`);
      if (active?.navigation_id === currentID) {
        try { runtime.storage?.removeItem(pendingKey); } catch {}
        active = null;
      }
    }).catch(() => {}).finally(() => { postPromise = null; });
    return postPromise;
  }

  return { begin, stateFetchStarted, stateFetchEnded, eventSourceConstructed, eventSourceOpened, snapshotStarted, surfaceReady };
}

const runtime = browserRuntime();
const telemetry = runtime ? createNavigationTelemetry(runtime) : null;
export const beginNavigation = (details) => telemetry?.begin(details);
export const navigationStateFetchStarted = (path) => telemetry?.stateFetchStarted(path) || null;
export const navigationStateFetchEnded = (sample) => telemetry?.stateFetchEnded(sample);
export const navigationEventSourceConstructed = () => telemetry?.eventSourceConstructed();
export const navigationEventSourceOpened = () => telemetry?.eventSourceOpened();
export const navigationSnapshotStarted = () => telemetry?.snapshotStarted();
export const navigationSurfaceReady = (surface, state) => telemetry?.surfaceReady(surface, state);
