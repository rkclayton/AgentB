import { api, reduce, setActive, store, subscribe } from "./bus.js";
import { initShell } from "./shell.js";
import { renderRail } from "./rail.js";
import { renderFlow } from "./flow.js";
import { renderRack } from "./rack.js";
import { renderState } from "./state.js";
import { renderTimeline } from "./timeline.js";
import { initSettings } from "./settings.js";
import { createMessageDropController } from "./message-drop.js";
import { createApprovalCard } from "./approval.js";
import { agentKey, lifetimeRows, ratio } from "./console-lifetime.js";
import { renderStopState } from "./stop-state.js";
import { navigationSurfaceReady } from "./navigation-telemetry.js";

const requestedSession = new URLSearchParams(location.search).get("session");
let initialSession = requestedSession;
let selectedAgent = "";
let ledger = null;
let renderFrame = 0;
const liveContent = document.getElementById("console-live-content");
const liveEmpty = document.getElementById("console-live-empty");
const dropLastMessage = document.getElementById("drop-last-message");
const agentSelect = document.getElementById("console-agent");
const agentProfileSelect = document.getElementById("console-agent-profile");
const feedback = document.getElementById("console-feedback");
const consoleStop = document.getElementById("console-stop");

initShell({ page: "console", reportError: showError });
initSettings();
const dropControl = createMessageDropController(dropLastMessage, {
  session: () => store.sessions[store.active], interactive: () => !store.replay,
  confirmDrop: (message) => window.confirm(message),
  drop: (id) => api(`/api/sessions/${encodeURIComponent(id)}/messages/drop-last`, {}), reportError: showError,
});

agentSelect.addEventListener("change", () => {
  selectedAgent = agentSelect.value;
  const current = store.sessions[store.active];
  if (current?.agent_id !== selectedAgent) {
    const next = Object.values(store.sessions).filter((session) => !session.closed && session.agent_id === selectedAgent)
      .sort((a, b) => Date.parse(b.created_at || 0) - Date.parse(a.created_at || 0))[0];
    if (next) setActive(next.id);
  }
  void refreshLedger();
});
agentProfileSelect.addEventListener("change", () => void bindAgentProfile());
document.getElementById("clear-stats").addEventListener("click", () => void clearStats());
document.getElementById("flush-memory").addEventListener("click", () => void flushMemory());
document.getElementById("console-tools").addEventListener("change", (event) => void toggleTool(event));
document.getElementById("console-tools-link").addEventListener("click", (event) => { event.preventDefault(); document.getElementById("console-tools-panel").scrollIntoView({block:"start"}); });
consoleStop.addEventListener("click", () => { const id=store.selection.session_id; if(id&&!store.replay) void api("/api/stop",{session_id:id}); });

subscribe((_state, event) => {
  if (event.type === "snapshot" && initialSession && store.sessions[initialSession]) {
    const id = initialSession; initialSession = ""; setActive(id); return;
  }
  if (!selectedAgent || ["selection.changed", "active.changed"].includes(event.type)) selectedAgent = store.sessions[store.active]?.agent_id || agentKey(store.config.agents?.[0]);
  if (event.type === "projection.patch" && (event.data?.operations || []).some((operation) => operation.path === "/run/partial" || /^\/chat\/[^/]+\/(reasoning|text)$/.test(operation.path))) {
    if (!liveContent.hidden) scheduleFlowRender();
    return;
  }
  if (["snapshot", "config.changed"].includes(event.type) || (event.type === "projection.patch" && patchEndedRun(event.data))) void refreshLedger(false);
  scheduleRender();
});

function scheduleRender() {
  if (!renderFrame) renderFrame = requestAnimationFrame(renderConsole);
}
let flowFrame = 0;
function scheduleFlowRender() {
  if (flowFrame) return;
  flowFrame = requestAnimationFrame(() => { flowFrame = 0; renderFlow(); });
}
setInterval(() => {
  const session = store.sessions[store.active];
  if (!liveContent.hidden && session?.run?.status === "running" && session.activity?.stage === "call_model") scheduleFlowRender();
}, 1000);

async function refreshLedger(render = true) {
  if (!selectedAgent || store.replay) { ledger = null; if (render) scheduleRender(); return; }
  try { ledger = await api(`/api/stats/${encodeURIComponent(selectedAgent)}`, undefined, "GET"); }
  catch (error) { showError(error.message); }
  if (render) scheduleRender();
}

function renderConsole() {
  renderFrame = 0;
  const agents = store.config.agents || [];
  if (!agents.some((agent) => agentKey(agent) === selectedAgent)) selectedAgent = agentKey(agents[0]);
  agentSelect.replaceChildren(...agents.map((agent) => option(agentKey(agent), agent.name, agentKey(agent) === selectedAgent)));
  const agent = agents.find((candidate) => agentKey(candidate) === selectedAgent);
  agentProfileSelect.replaceChildren(...(store.servers || []).map((profile) => option(profile.id, profile.label, profile.id === agent?.b)));
  agentProfileSelect.disabled = !agent || store.replay;
  document.getElementById("console-agent-binding").textContent = agent ? `${agent.c ? `c ${agent.c}` : ""}${agent.d ? `${agent.c ? " · " : ""}d ${agent.d}` : ""}` : "No configured agents";
  renderTools(agent);
  renderLifetime();
  const session = store.sessions[store.active];
  const hasSelectedChat = !!session && session.agent_id === selectedAgent;
  liveContent.hidden = !hasSelectedChat;
  liveEmpty.hidden = hasSelectedChat;
  renderStopState(consoleStop, hasSelectedChat ? session : null, store.replay);
  document.getElementById("console-live-state").textContent = !hasSelectedChat ? "no open chat" : session.pending_approval ? "waiting for you" : session.run?.status || "idle";
  if (hasSelectedChat) {
    renderRail(); renderFlow(); renderRack(); renderState(); renderTimeline(); placeDropLastMessage(); dropControl.render(); renderPendingApproval(session);
  }
  navigationSurfaceReady("console", store);
}

async function bindAgentProfile() {
  const agents = store.config.agents || [];
  const index = agents.findIndex((agent) => agentKey(agent) === selectedAgent);
  if (index < 0 || !agentProfileSelect.value) return;
  const updated = agents.map((agent, offset) => offset === index ? { ...agent, b: agentProfileSelect.value } : agent);
  try {
    const config = await api("/api/config", { agents: updated });
    reduce({ type: "config.changed", data: { config } });
    showFeedback(`Agent B profile changed to ${agentProfileSelect.selectedOptions[0]?.textContent || agentProfileSelect.value}. New chats use this connection.`);
  } catch (error) {
    showError(error.message);
    scheduleRender();
  }
}

function renderTools(agent) {
  const root = document.getElementById("console-tools");
  if (!agent) { root.innerHTML = '<p class="console-empty">No agent selected.</p>'; return; }
  const enabled = new Set(agent.toolset || []);
  document.getElementById("console-tools-link").textContent = `${enabled.size} tools active`;
  const counters = ledger?.agent?.tools || {};
  root.replaceChildren(...(store.tools || []).map((tool) => {
    const stats = counters[tool.name] || {};
    const row = node("label", "console-line console-tool-line");
    const toggle = document.createElement("input");
    toggle.type = "checkbox"; toggle.checked = enabled.has(tool.name); toggle.dataset.tool = tool.name; toggle.disabled = store.replay;
    row.append(toggle, text(tool.name), text(stats.calls || 0), text(ratio(stats.failures || 0, stats.calls || 0)), text(stats.last_used || "—"));
    return row;
  }));
}

function renderLifetime() {
  const root = document.getElementById("console-stats");
  if (!ledger) { root.innerHTML = '<p class="console-empty">No lifetime activity.</p>'; return; }
  const sections = [[selectedAgent, ledger.agent], ...Object.entries(ledger.profiles || {})];
  root.replaceChildren(...sections.flatMap(([name, counters]) => [text(name, "console-profile-head"), ...lifetimeRows(counters, percentile).map(([label, value]) => line(label, value))]));
}

async function toggleTool(event) {
  const name = event.target?.dataset?.tool;
  if (!name || !selectedAgent) return;
  try { await api(`/api/tools/${encodeURIComponent(name)}`, { agent_id: selectedAgent, enabled: event.target.checked }); await refreshState(); }
  catch (error) { event.target.checked = !event.target.checked; showError(error.message); }
}

async function clearStats() {
  if (!selectedAgent || store.replay || !window.confirm(`Clear all lifetime stats for ${selectedAgent}?`)) return;
  try { ledger = await api(`/api/stats/${encodeURIComponent(selectedAgent)}/clear`, { confirm: true }); showFeedback(`Stats cleared for ${selectedAgent}.`); scheduleRender(); }
  catch (error) { showError(error.message); }
}

async function flushMemory() {
  const session = Object.values(store.sessions).find((item) => item.agent_id === selectedAgent && !item.closed) || store.sessions[store.active];
  if (!session || store.replay) return showError("An open chat is required to identify the workspace.");
  try {
    const preview = await api(`/api/agents/${encodeURIComponent(selectedAgent)}/memory/flush`, { workspace: session.workspace, confirm: false });
    if (!window.confirm(`Flush memory for ${selectedAgent} and ${preview.workspace}?\n\n${preview.agent_entries} agent entries and ${preview.workspace_entries} workspace entries will be removed.`)) return;
    await api(`/api/agents/${encodeURIComponent(selectedAgent)}/memory/flush`, { workspace: session.workspace, confirm: true });
    showFeedback(`Memory flushed for ${selectedAgent} and ${preview.workspace}.`); await refreshState();
  } catch (error) { showError(error.message); }
}

async function refreshState() { reduce({ type: "snapshot", data: await api("/api/state", undefined, "GET") }); await refreshLedger(); }
function placeDropLastMessage() { const heads = document.querySelectorAll(".timeline-model > .timeline-head"); const target = heads[heads.length - 1]; dropLastMessage.hidden = !target; if (target) target.append(dropLastMessage); }
function renderPendingApproval(session) { const root = document.getElementById("console-pending-approval"); root.hidden = !session?.pending_approval; root.replaceChildren(...(session?.pending_approval ? [createApprovalCard(document, session.pending_approval, { replay: store.replay, decide: (callID, decision) => api("/api/approve", { session_id: session.id, call_id: callID, decision }) })] : [])); }
function percentile(values = [], fraction = .5) { if (!values.length) return 0; const copy = [...values].sort((a, b) => a - b); return copy[Math.floor((copy.length - 1) * fraction)]; }
function patchEndedRun(patch = {}) { return (patch.operations || []).some((operation) => operation.path === "/run" && ["idle", "held"].includes(operation.value?.status)); }
function line(label, value) { const row = node("div", "console-line"); row.append(text(label), text(String(value))); return row; }
function node(tag, className = "") { const value = document.createElement(tag); value.className = className; return value; }
function text(value, className = "") { const result = node("span", className); result.textContent = String(value); return result; }
function button(value, title) { const result = node("button"); result.type = "button"; result.textContent = value; result.title = title; return result; }
function option(value, label, selected) { const result = document.createElement("option"); result.value = value; result.textContent = label; result.selected = selected; return result; }
function showFeedback(message) { feedback.textContent = message; feedback.className = ""; }
function showError(message) { feedback.textContent = message; feedback.className = "alarm"; const connection = document.getElementById("connection"); if (connection) { connection.textContent = message; connection.className = "alarm"; } }
