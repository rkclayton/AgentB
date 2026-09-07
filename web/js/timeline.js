import { api, store } from "./bus.js";
import { createPanelState } from "./panel-state.js";
import { groupToolRuns, toolGroupRange, toolGroupStatus, toolResultText } from "./timeline-groups.js";
import { operatorLogEntry } from "./operator-log.js";
import { callServiceKey, callServiceStatus } from "./call-service-display.js";
import { createFileChip, fileURL, probeFile } from "./deliverables.js";
import { formatDuration } from "./duration.js";

const states = createPanelState("timeline");
const attachmentStates = new Map();
let rendered = "";
const root = document.getElementById("timeline-list"),
  count = document.getElementById("timeline-count");
function view(id) {
  return states.view(id);
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
    turns = session.model_turns || 0,
    compactions = session.compaction_count || 0,
    compactedTokens = session.compaction_token_delta || 0,
    summaryCalls = session.compaction_model_calls || 0,
    summaryPrompt = session.compaction_prompt_tokens || 0,
    summaryCompletion = session.compaction_completion_tokens || 0;
  count.textContent = `${turns} turns${compactions ? ` · ${compactions} compact · ${formatSigned(compactedTokens)} context` : ""}${summaryCalls ? ` · ${summaryCalls} summary ${formatNumber(summaryPrompt)} in/${formatNumber(summaryCompletion)} out` : ""}`;
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
    else if (event.type === "compaction.summary" && event.data?.outcome !== "accepted")
      entries.push({ kind: event.type, event });
    else if (event.type === "message.appended" && event.data?.message?.role === "user" && event.data?.message?.attachments?.length)
      entries.push({ kind: event.type, event });
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
  const groupedEntries = groupToolRuns(entries),
    hidden = Math.max(0, groupedEntries.length - 300),
    shown = groupedEntries.slice(hidden);
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
    else if (entry.kind === "tool-group")
      root.append(toolGroupRow(session, entry, calls, results, decisions, state));
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
function toolGroupRow(session, group, calls, results, decisions, state) {
  const status = toolGroupStatus(group, results),
    key = `tool-group:${group.firstCallID}`,
    row = baseRow(
      key,
      state,
      `timeline-tool-group ${status.failed ? "error" : ""} ${status.pending ? "active" : ""} ${status.untrusted ? "untrusted" : ""}`,
    ),
    range = toolGroupRange(group, calls);
  row.head.innerHTML = '<span class="timeline-lamp"></span><span class="timeline-label"></span><span class="timeline-group-count number"></span><span class="timeline-key"></span><span class="duration number"></span><span class="finish"></span>';
  row.head.children[1].textContent = friendly(group.tool);
  row.head.children[2].textContent = `×${status.ids.length}`;
  row.head.children[3].textContent = formatGroupRange(range);
  row.head.children[4].textContent = formatDuration(status.duration);
  row.head.children[5].textContent = status.failed
    ? `${status.failed} failed`
    : status.pending
      ? "Running"
      : status.untrusted
        ? "Untrusted"
        : "Done";
  for (const item of group.items) {
    if (item.kind === "model") {
      const call = item.event.data.tool_calls[0],
        child = toolRow(
          session,
          call,
          calls.get(call.id),
          results.get(call.id),
          state,
          item.event,
        );
      child.classList.add("timeline-group-call");
      child.querySelector(".timeline-label").textContent = `Turn ${item.event.data.turn || ""} · ${friendly(call.name)}`;
      row.expansion.append(child);
    } else {
      row.expansion.append(inlineRow(session, item.event, decisions, state));
    }
  }
  return row.node;
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
function toolRow(session, call, callEvent, resultEvent, state, modelEvent = null) {
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
  row.head.children[3].textContent = formatDuration(result.ms);
  const serviceStatus = callServiceStatus(call.name, result);
  row.head.children[4].textContent = serviceStatus || (result.operator_context
    ? `Operator · ${result.ok === false ? "Failed" : "Done"}`
    : result.ok === false
      ? "Failed"
      : "Done");
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
  ), resultText = toolResultText(message, result, originalToolResult(session, call.id));
  addText(
    row.expansion,
    "result",
    modelEvent ? resultText : capLines(resultText),
  );
  if (modelEvent) {
    const model = modelEvent.data || {},
      assistant = [...(session.messages || [])]
        .reverse()
        .find((item) => item.role === "assistant" && item.turn === model.turn);
    addBlock(row.expansion, "turn", {
      turn: model.turn,
      duration_ms: model.duration_ms,
      finish_reason: model.finish_reason,
      usage: model.usage,
      params: model.params || findRequest(session, modelEvent)?.data?.params || {},
      timings: model.timings,
    });
    if (model.content || assistant?.content)
      addText(row.expansion, "content", model.content || assistant.content);
    if (assistant?.reasoning)
      addText(row.expansion, "thinking", assistant.reasoning);
  }
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
    const shellPolicy = !boundaryEscape && data.name === "shell";
    const path = data.args?.path || "";
    text.textContent = boundaryEscape
      ? `Privilege escalation · ${data.args?.reason || "Service identity denied the operation"}`
      : shellPolicy
        ? `Run shell command · ${keyArgument(data.args || {})}`
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
  } else if (event.type === "message.appended" && data.message?.attachments?.length) {
    text.textContent = `Attached · ${data.message.attachments.length} file${data.message.attachments.length === 1 ? "" : "s"}`;
    const chips = document.createElement("div");
    chips.className = "timeline-attachment-chips";
    for (const attachment of data.message.attachments) chips.append(timelineAttachmentChip(session, attachment));
    row.expansion.append(chips);
  } else if (event.type === "run.queued") {
    text.textContent = `Waiting · position ${data.position}`;
  } else if (event.type === "operator.context") {
    const entry = operatorLogEntry(data);
    text.textContent = entry.text;
    if (entry.alarm) row.node.classList.add("operator-mode-enabled");
  } else if (event.type === "compaction") {
    const affected = data.affected_ids?.length || "",
      delta = (data.after || 0) - (data.before || 0),
      served = data.profile_id ? ` · ${friendly(data.role)} ${data.profile_id}${data.model ? `/${data.model}` : ""}` : "",
      usage = data.usage ? ` · ${formatNumber(data.usage.prompt_tokens)} in/${formatNumber(data.usage.completion_tokens)} out` : "",
      fallback = data.fallback_reason ? ` · fallback ${friendly(data.fallback_reason)}` : "";
    text.textContent = `Compacted · ${friendly(data.kind)} · ${formatNumber(data.before)} → ${formatNumber(data.after)} · ${formatSigned(delta)} tokens${affected ? ` · ${affected} items` : ""}${served}${usage}${fallback}`;
  } else if (event.type === "compaction.summary") {
    text.textContent = `Summary ${friendly(data.outcome)} · ${friendly(data.role)} ${data.profile_id || "unknown"}${data.model ? `/${data.model}` : ""}${data.reason ? ` · ${data.reason}` : ""}`;
    if (data.outcome === "error") row.node.classList.add("fault");
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
  head.setAttribute("aria-expanded", String(state.expanded.has(key)));
  head.onclick = () => {
    state.toggle(key);
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

function timelineAttachmentChip(session, attachment) {
  const key = `${session.id}:${attachment.path.toLowerCase()}:${attachment.sha256}`;
  let state = attachmentStates.get(key);
  if (!state) {
    state = { state: "checking", bytes: attachment.bytes };
    attachmentStates.set(key, state);
    probeFile(fileURL(session.id, attachment.path)).then((next) => {
      attachmentStates.set(key, next);
      renderTimeline();
    });
  }
  return createFileChip(document, attachment, state, {
    downloadURL: fileURL(session.id, attachment.path),
    openFolder: () => api("/api/open-folder", { session_id: session.id, path: attachment.path, scope: "workspace" }),
  });
}
function originalToolResult(session, callID) {
  return (session.timeline || []).find(
    (event) =>
      event.type === "message.appended" &&
      event.data?.message?.tool_call_id === callID,
  )?.data?.message?.content || "";
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
function formatNumber(value) {
  return Number(value || 0).toLocaleString("en-US");
}
function formatSigned(value) {
  return `${value < 0 ? "−" : value > 0 ? "+" : "±"}${formatNumber(Math.abs(value))}`;
}

function formatGroupRange(range) {
  if (!range) return "";
  const label = range.key.endsWith("s") ? range.key : `${range.key}s`;
  if (range.numeric)
    return `${label} ${formatNumber(range.first)}–${formatNumber(range.last)}`;
  return `${label} ${range.first} → ${range.last}`.slice(0, 120);
}

function keyArgument(args) {
  if (!args || typeof args !== "object") return "";
  const service = callServiceKey(args);
  if (service) return service.slice(0, 120);
  for (const key of ["url", "path", "pattern", "query", "command", "note"]) {
    if (args[key]) return String(args[key]).replaceAll("\n", " ").slice(0, 120);
  }
  return "";
}

function friendly(value) {
  const text = String(value || "").replaceAll("_", " ").replaceAll(".", " ");
  return text ? text[0].toUpperCase() + text.slice(1) : "";
}
