import { createOperatorReconciler } from "./operator-reconcile.js";

export const store = {
  sessions: {}, active: "", servers: [], config: {}, flow: { stages: [], edges: [] }, tools: [], serving_facts: {},
  build: { tag: "", commit: "unknown", dirty: false, known: false, source: "unknown", display: "unknown" }, signature: {},
  mutation_token: "", shell_credential: { stored: false, stored_at: "" },
  shell_identity: { fallback: false, operator_approval_required: false, operator_context: false, reason: "", since: "" }, replay: false,
};
const listeners = new Set();
const operatorReconciler = createOperatorReconciler({
  readState: async () => {
    const response = await fetch("/api/state", { cache: "no-store" });
    if (!response.ok) throw new Error(`state reconciliation failed: HTTP ${response.status}`);
    return response.json();
  },
  applyIdentity: (identity) => reduce({ type: "shell.identity", data: identity }),
});
export function subscribe(fn) { listeners.add(fn); fn(store, { type: "init" }); return () => listeners.delete(fn); }
function notify(event) { for (const fn of listeners) fn(store, event); }

// Session state is server-authored. This client applies versioned projection
// operations; it does not fold raw domain events into session fields.
export function reduce(event) {
  const data = event.data || {};
  if (event.type === "snapshot") {
    const active = store.active;
    Object.assign(store, data);
    store.active = store.sessions[active] ? active : Object.keys(store.sessions)[0] || "";
    operatorReconciler.observed();
    notify(event);
    return;
  }
  if (event.type === "projection.patch") {
    applyProjectionPatch(data);
    notify(event);
    return;
  }
  switch (event.type) {
    case "server.probed": {
      const profile = store.servers.find((value) => value.id === data.server_id);
      if (profile) { profile.capabilities = data.capabilities; profile.reasoning.valid_efforts = data.capabilities?.valid_efforts || []; profile._probing = false; }
      const configured = store.config.servers?.find((value) => value.id === data.server_id);
      if (configured) { configured.capabilities = data.capabilities; configured.reasoning.valid_efforts = data.capabilities?.valid_efforts || []; }
      break;
    }
    case "config.changed": store.config = data.config; store.servers = data.config.servers || store.servers; break;
    case "shell.identity": store.shell_identity = data; operatorReconciler.observed(); break;
    case "shell.credential": store.shell_credential = data; break;
    case "operator.context":
      store.shell_identity = { ...store.shell_identity, operator_context: !!data.enabled,
        operator_context_expires_at: data.expires_at || "", reason: data.enabled ? data.reason || store.shell_identity.reason : "" };
      operatorReconciler.observed();
      break;
    case "error": store.error = data; break;
    default: return;
  }
  notify(event);
}

function applyProjectionPatch(patch) {
  if (patch.schema_version !== 1) { void resync(); return; }
  let target = store.sessions[patch.session_id];
  const previous = patch.previous_cursor || { generation: "", offset: 0 };
  if (target && !sameCursor(target.cursor, previous)) { void resync(); return; }
  if (!target) target = store.sessions[patch.session_id] = { id: patch.session_id, cursor: previous };
  for (const operation of patch.operations || []) {
    if (!applyOperation(target, operation)) { void resync(); return; }
  }
  target.cursor = patch.cursor;
  if (target.closed && !store.replay) {
    delete store.sessions[patch.session_id];
    if (store.active === patch.session_id) store.active = Object.keys(store.sessions)[0] || "";
  } else if (!store.active) store.active = patch.session_id;
}
function applyOperation(target, operation) {
  const parts = String(operation.path || "").split("/").slice(1).map((value) => value.replaceAll("~1", "/").replaceAll("~0", "~"));
  if (!parts.length || !parts[0]) return false;
  if (parts.length === 1) {
    const key = parts[0];
    if (operation.op === "replace") target[key] = operation.value;
    else if (operation.op === "append") {
      if (Array.isArray(target[key])) target[key].push(operation.value);
      else if (typeof target[key] === "string") target[key] += String(operation.value || "");
      else return false;
    } else if (operation.op === "delete") delete target[key];
    else return false;
    return true;
  }
  if (parts.length === 2 && operation.op === "upsert" && Array.isArray(target[parts[0]])) {
    const index = target[parts[0]].findIndex((value) => value.key === parts[1] || value.id === parts[1] || value.name === parts[1]);
    if (index >= 0) target[parts[0]][index] = operation.value;
    else target[parts[0]].push(operation.value);
    return true;
  }
  let parent = target[parts[0]];
  if (Array.isArray(parent)) parent = parent.find((value) => value.key === parts[1] || value.id === parts[1] || value.name === parts[1]);
  else parent = parent?.[parts[1]];
  const key = parts[2];
  if (!parent || !key) return false;
  if (operation.op === "replace") parent[key] = operation.value;
  else if (operation.op === "append" && typeof parent[key] === "string") parent[key] += String(operation.value || "");
  else return false;
  return true;
}
function sameCursor(left = {}, right = {}) {
  return (left.generation || "") === (right.generation || "") && Number(left.offset || 0) === Number(right.offset || 0);
}
let resyncing = false;
async function resync() {
  if (resyncing) return;
  resyncing = true;
  try { reduce({ type: "snapshot", data: await api("/api/state", undefined, "GET") }); }
  finally { resyncing = false; }
}

export function setActive(id) { if (store.sessions[id]) { store.active = id; notify({ type: "active.changed", data: { id } }); } }
export async function api(path, body, method = "POST") {
  const options = { method, headers: {} };
  if (method !== "GET" && method !== "HEAD") options.headers["X-AgentB-Mutation-Token"] = store.mutation_token;
  if (body !== undefined) { options.headers["Content-Type"] = "application/json"; options.body = JSON.stringify(body); }
  const response = await fetch(path, options);
  const data = await response.json().catch(() => ({}));
  if (!response.ok) { const error = new Error(data.error || `HTTP ${response.status}`); error.field = data.field || ""; error.status = response.status; error.data = data; throw error; }
  return data;
}

const connection = document.getElementById("connection");
let reconnected = false;
const eventSearch = typeof location === "undefined" ? "" : location.search;
const eventURL = new URLSearchParams(eventSearch).get("instant") === "1" ? "/api/events?instant=1" : "/api/events";
const source = new EventSource(eventURL);
source.onopen = () => {
  if (reconnected && connection) { connection.textContent = "reconnected"; connection.className = ""; setTimeout(() => { connection.textContent = ""; }, 3000); }
  reconnected = true;
  void operatorReconciler.reconcile().catch(() => {});
};
source.onerror = () => { if (connection) { connection.textContent = "connection lost — retrying"; connection.className = "alarm"; } };
source.onmessage = (event) => reduce(JSON.parse(event.data));
for (const type of ["snapshot", "projection.patch", "server.probed", "config.changed", "shell.identity", "shell.credential", "operator.context", "error"])
  source.addEventListener(type, (event) => reduce(JSON.parse(event.data)));
function reconcileVisibleClient() { if (!document.hidden) void operatorReconciler.reconcile().catch(() => {}); }
document.addEventListener("visibilitychange", reconcileVisibleClient);
window.addEventListener("focus", reconcileVisibleClient);
window.addEventListener("pageshow", reconcileVisibleClient);
