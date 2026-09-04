import { api, store } from "./bus.js";

const states = new Map();
let rendered = "";
const root = document.getElementById("timeline-list"),
  count = document.getElementById("timeline-count");
function view(id) {
  if (!states.has(id))
    states.set(id, { expanded: new Set(), scroll: 0, follow: true });
  return states.get(id);
}
function save() {
  if (!rendered) return;
  const state = view(rendered);
  state.scroll = root.scrollTop;
  state.follow = root.scrollHeight - root.clientHeight - root.scrollTop <= 24;
}
export function renderTimeline() {
  save();
  const session = store.sessions[store.active];
  rendered = store.active;
  root.replaceChildren();
  if (!session) {
    count.textContent = "";
    return;
  }
  const state = view(session.id),
    events = session.timeline || [],
    turns = events.filter((event) => event.type === "model.response").length;
  count.textContent = String(turns);
  const calls = new Map(),
    results = new Map(),
    decisions = new Map();
  for (const event of events) {
    if (event.type === "tool.call") calls.set(event.data.call_id, event);
    if (event.type === "tool.result") results.set(event.data.call_id, event);
    if (event.type === "approval.decided")
      decisions.set(event.data.call_id, event.data.decision);
  }
  const entries = [];
  for (const event of events) {
    if (event.type === "model.response") entries.push({ kind: "model", event });
    else if (
      [
        "approval.required",
        "workspace.conflict",
        "message.queued",
        "run.queued",
        "operator.context",
        "compaction",
        "run.stopped",
      ].includes(event.type)
    )
      entries.push({ kind: event.type, event });
  }
  const hidden = Math.max(0, entries.length - 300),
    shown = entries.slice(hidden);
	if (!entries.length) {
	  const empty = document.createElement("div");
	  empty.className = "panel-empty";
	  empty.textContent = "—";
	  root.append(empty);
	}
  if (hidden) {
    const earlier = document.createElement("div");
    earlier.className = "timeline-earlier";
    earlier.textContent = `earlier: ${hidden} entries`;
    root.append(earlier);
  }
  for (const entry of shown) {
    if (entry.kind === "model")
      root.append(modelRow(session, entry.event, calls, results, state));
    else root.append(inlineRow(session, entry.event, decisions, state));
  }
  const jump = document.createElement("button");
  jump.type = "button";
  jump.className = "jump-latest";
  jump.textContent = "Jump to latest";
  jump.hidden = true;
  jump.onclick = () => {
    state.follow = true;
    renderTimeline();
  };
  root.append(jump);
  requestAnimationFrame(() => {
    if (state.follow) root.scrollTop = root.scrollHeight;
    else root.scrollTop = state.scroll;
    jump.hidden = state.follow;
  });
}
function modelRow(session, event, calls, results, state) {
  const data = event.data || {},
    key = `${event.run_id}:model:${data.turn}`,
    row = baseRow(key, state, "timeline-model");
  const usage = data.usage || {};
  row.head.innerHTML = '<span class="timeline-label"></span><span class="duration number"></span><span class="usage number"></span><span class="finish"></span>';
  row.head.children[0].textContent = `Turn ${data.turn || ""}`;
  row.head.children[1].textContent = formatDuration(data.duration_ms);
  row.head.children[2].textContent = `${formatNumber(usage.prompt_tokens)} in · ${formatNumber(usage.completion_tokens)} out`;
  row.head.children[3].textContent = friendly(data.finish_reason || "done");
  const assistant = [...(session.messages || [])]
    .reverse()
    .find(
      (message) => message.role === "assistant" && message.turn === data.turn,
    );
  addBlock(
    row.expansion,
    "params",
    data.params || findRequest(session, event)?.data?.params || {},
  );
  addBlock(row.expansion, "usage", usage);
  if (data.timings) addBlock(row.expansion, "timings", data.timings);
  if (data.content || assistant?.content)
    addText(row.expansion, "content", data.content || assistant.content);
  if (assistant?.reasoning)
    addText(row.expansion, "thinking", assistant.reasoning);
  for (const call of data.tool_calls || []) {
    row.node.append(
      toolRow(session, call, calls.get(call.id), results.get(call.id), state),
    );
  }
  return row.node;
}
function toolRow(session, call, callEvent, resultEvent, state) {
  const result = resultEvent?.data || {},
    key = `tool:${call.id}`,
    row = baseRow(
      key,
      state,
      `timeline-tool ${result.ok === false ? "error" : ""}`,
    );
  const args = callEvent?.data?.args || safeJSON(call.arguments);
  row.head.innerHTML = '<span class="timeline-lamp"></span><span class="timeline-label"></span><span class="timeline-key"></span><span class="duration number"></span><span class="finish"></span>';
  row.head.children[1].textContent = friendly(call.name);
  row.head.children[2].textContent = keyArgument(args);
  row.head.children[3].textContent = formatDuration(result.ms || 0);
  row.head.children[4].textContent = result.operator_context
    ? `Operator · ${result.ok === false ? "Failed" : "Done"}`
    : result.ok === false
      ? "Failed"
      : "Done";
  if (result.untrusted) {
    row.node.classList.add("untrusted");
    row.head.children[4].textContent = `Untrusted · ${result.ok === false ? "Failed" : "Done"}`;
  }
  addBlock(
    row.expansion,
    "arguments",
    args,
  );
  const message = (session.messages || []).find(
    (item) => item.tool_call_id === call.id,
  );
  addText(
    row.expansion,
    "result",
    capLines(message?.content || result.preview || ""),
  );
  return row.node;
}
function inlineRow(session, event, decisions, state) {
  const data = event.data || {},
    key = `${event.type}:${event.seq}`,
    row = baseRow(
      key,
      state,
      `timeline-inline ${event.type.replaceAll(".", "-")}`,
    ),
    text = document.createElement("span");
  if (event.type === "run.stopped" && data.reason !== "done") {
    row.node.classList.add("fault");
  }
  row.head.replaceChildren(text);
  if (event.type === "approval.required") {
    const boundaryEscape = typeof data.boundary_escape === "boolean"
      ? data.boundary_escape
      : data.name?.endsWith(".operator_override");
    const path = data.args?.path || "";
    text.textContent = boundaryEscape
      ? `Privilege escalation · ${data.args?.reason || "Service identity denied the operation"}`
      : `Policy confirmation · ${friendly(data.name)} ${path}`;
    if (boundaryEscape) row.node.classList.add("fault");
    const decision = decisions.get(data.call_id);
    if (decision) {
      const decided = document.createElement("span");
      decided.className = "decision";
      decided.textContent = decision;
      row.head.append(decided);
    } else
      for (const choice of boundaryEscape
        ? [["approve", "Run once as operator"], ["deny", "Keep denied"]]
        : [["approve", "Approve"], ["deny", "Deny"]]) {
        const [value, label] = choice;
        const button = document.createElement("button");
        button.type = "button";
        button.textContent = label;
        button.onclick = (eventClick) => {
          eventClick.stopPropagation();
          api("/api/approve", {
            session_id: session.id,
            call_id: data.call_id,
            decision: value,
          });
        };
        row.head.append(button);
      }
    addBlock(row.expansion, "arguments", data.args || {});
  } else if (event.type === "workspace.conflict") {
    text.textContent = `Conflict · ${data.path} · ${data.other_label} · ${data.age_s} s`;
  } else if (event.type === "message.queued") {
    text.textContent = `Queued · message ${String(data.message_id || "").replace(/\D/g, "")} · position ${data.position}`;
  } else if (event.type === "run.queued") {
    text.textContent = `Waiting · position ${data.position}`;
  } else if (event.type === "operator.context") {
    text.textContent = data.enabled
      ? `Operator context · on${data.expires_at ? ` · until ${new Date(data.expires_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}` : ""}`
      : `Operator context · off · ${data.reason || "operator request"}`;
    if (data.enabled) row.node.classList.add("fault");
  } else if (event.type === "compaction") {
    const affected = data.affected_ids?.length || "",
      delta = (data.after || 0) - (data.before || 0);
    text.textContent = `Compacted · ${friendly(data.kind)}${affected ? ` · ${affected} results` : ""} · ${formatSigned(delta)} tokens`;
  } else if (event.type === "run.stopped") {
    const label = (data.reason || "").replaceAll("_", " ");
    text.textContent = `Stopped · ${label}${data.detail ? ` · ${data.detail}` : ""}`;
  }
  addBlock(row.expansion, "detail", data);
  return row.node;
}
function baseRow(key, state, className) {
  const node = document.createElement("div");
  node.className = `timeline-row ${className} ${state.expanded.has(key) ? "expanded" : ""}`;
  const head = document.createElement("button");
  head.type = "button";
  head.className = "timeline-head";
  head.onclick = () => {
    state.expanded.has(key)
      ? state.expanded.delete(key)
      : state.expanded.add(key);
    renderTimeline();
  };
  const expansion = document.createElement("div");
  expansion.className = "row-expansion";
  node.append(head, expansion);
  return { node, head, expansion };
}
function addBlock(root, label, value) {
  const title = document.createElement("div");
  title.className = "detail-label";
  title.textContent = label;
  const pre = document.createElement("pre");
  pre.textContent = JSON.stringify(value, null, 2);
  root.append(title, pre);
}
function addText(root, label, value) {
  const title = document.createElement("div");
  title.className = "detail-label";
  title.textContent = label;
  const pre = document.createElement("pre");
  pre.textContent = value;
  root.append(title, pre);
}
function findRequest(session, response) {
  return [...(session.timeline || [])]
    .reverse()
    .find(
      (event) =>
        event.type === "model.request" &&
        event.run_id === response.run_id &&
        event.data?.turn === response.data?.turn,
    );
}
function safeJSON(value) {
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
}
function capLines(value) {
  const lines = String(value).split("\n");
  return lines.length <= 200
    ? value
    : [...lines.slice(0, 199), "[… open the JSONL for the rest]"].join("\n");
}
function formatDuration(ms) {
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${ms || 0} ms`;
}
function formatNumber(value) {
  return Number(value || 0).toLocaleString("en-US");
}
function formatSigned(value) {
  return `${value < 0 ? "−" : value > 0 ? "+" : "±"}${formatNumber(Math.abs(value))}`;
}

function keyArgument(args) {
  if (!args || typeof args !== "object") return "";
  for (const key of ["url", "path", "pattern", "query", "command", "note"]) {
    if (args[key]) return String(args[key]).replaceAll("\n", " ").slice(0, 120);
  }
  return "";
}

function friendly(value) {
  const text = String(value || "").replaceAll("_", " ").replaceAll(".", " ");
  return text ? text[0].toUpperCase() + text.slice(1) : "";
}
