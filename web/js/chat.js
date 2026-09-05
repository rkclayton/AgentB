import { api, store, subscribe } from "./bus.js";
import { createOperatorStatusController, isOperatorStateEvent } from "./operator-status.js";
import { createThinkingRenderer, hydrateAgentEntries, modelTurnKey } from "./reasoning.js";

const binding = document.getElementById("chat-binding");
const status = document.getElementById("chat-status");
const stop = document.getElementById("chat-stop");
const operatorStatus = document.getElementById("chat-operator-status");
const budget = document.getElementById("chat-budget");
const log = document.getElementById("chat-log");
const input = document.getElementById("chat-task");
const send = document.getElementById("chat-send");
const notice = document.getElementById("chat-notice");
const identityAlarm = document.getElementById("chat-identity-alarm");
const chatCurrent = document.getElementById("chat-current");
const consoleButton = document.getElementById("chat-console");
const settingsButton = document.getElementById("chat-settings");
const requested = new URLSearchParams(location.search).get("session");
const expanded = new Set();
let bound = requested || "";
let follow = true;
let page = 0;
let localNotice = "";
let localAlarm = false;
let frame = 0;
let renderTimer = 0;
let lastRender = 0;
const renderIntervalMS = 50;
const thinkingRenderer = createThinkingRenderer({
  document,
  expanded,
  rerender: () => render(),
  format,
  formatDuration: formatThoughtSeconds,
});
const entryViews = new Map();
let usedEntryViews = new Set();
const earlierButton = document.createElement("button");
earlierButton.type = "button";
earlierButton.className = "chat-earlier";
earlierButton.onclick = () => {
  page++;
  follow = false;
  renderLog(store.sessions[bound]);
};
const jumpButton = document.createElement("button");
jumpButton.type = "button";
jumpButton.className = "chat-jump";
jumpButton.textContent = "Jump to latest";
jumpButton.onclick = () => {
  follow = true;
  page = 0;
  renderLog(store.sessions[bound]);
};
const operatorControl = createOperatorStatusController(operatorStatus, {
  identity: () => store.shell_identity,
  interactive: () => !store.replay,
  confirmEnable: () => window.confirm("Enable operator mode? Tools will run as your Windows account until you turn it off or it expires."),
  setOperatorContext: (enabled) => api("/api/config", { shell: { operator_context: enabled } }),
  reportError: (message) => {
    localNotice = message;
    localAlarm = true;
    renderComposer(store.sessions[bound]);
  },
});

chatCurrent.addEventListener("click", (event) => event.preventDefault());

function consoleURL() {
  const query = new URLSearchParams();
  if (bound) query.set("session", bound);
  return `/${query.size ? `?${query}` : ""}`;
}

subscribe((_state, event) => {
  if (isOperatorStateEvent(event)) operatorControl.render();
  if (event.type === "snapshot") {
    if (!store.sessions[bound]) bound = requested && store.sessions[requested] ? requested : Object.keys(store.sessions)[0] || "";
  }
  if (event.session_id && bound && event.session_id !== bound) return;
  schedule();
});

function schedule() {
  if (frame || renderTimer) return;
  const delay = Math.max(0, renderIntervalMS - (performance.now() - lastRender));
  renderTimer = setTimeout(() => {
    renderTimer = 0;
    frame = requestAnimationFrame(() => {
      frame = 0;
      lastRender = performance.now();
      render();
    });
  }, delay);
}

function render() {
  const session = store.sessions[bound];
  const query = new URLSearchParams();
  if (bound) query.set("session", bound);
  chatCurrent.href = `/chat${query.size ? `?${query}` : ""}`;
  consoleButton.href = consoleURL();
  settingsButton.href = `${consoleURL()}#settings/servers`;
  renderIdentityAlarm();
  renderBinding(session);
  renderHeader(session);
  renderBudget(session);
  renderLog(session);
  renderComposer(session);
}

function renderIdentityAlarm() {
  const unavailable = store.shell_identity?.operator_approval_required || store.shell_identity?.fallback;
  operatorControl.render();
  identityAlarm.hidden = !unavailable;
  identityAlarm.textContent = unavailable
    ? `SERVICE IDENTITY UNAVAILABLE — tools require explicit operator approval: ${store.shell_identity.reason}`
    : "";
}

function renderBinding(session) {
  binding.replaceChildren();
  if (requested) {
    binding.textContent = session?.label || requested;
    return;
  }
  const select = document.createElement("select");
  select.setAttribute("aria-label", "Session");
  for (const item of Object.values(store.sessions)) {
    const option = document.createElement("option");
    option.value = item.id;
    option.textContent = item.label;
    option.selected = item.id === bound;
    select.append(option);
  }
  select.onchange = () => {
    bound = select.value;
    follow = true;
    page = 0;
    render();
  };
  binding.append(select);
}

function renderHeader(session) {
  status.textContent = store.replay ? "replay" : session?.run?.status || "idle";
  stop.hidden = !!store.replay;
  stop.disabled = !session || !busy(session);
}

function renderBudget(session) {
  const value = session?.budget || {};
  const used = value.used_measured || value.used_est || 0;
  const ceiling = value.ceiling || 0;
  const ratio = ceiling ? used / ceiling : 0;
  budget.className = `chat-budget ${ratio > 1 ? "over" : ratio > 0.85 ? "warn" : ""}`;
  budget.querySelector(".chat-budget-fill").style.width = `${Math.min(100, ratio * 100)}%`;
  budget.querySelector(".chat-budget-tip").textContent = `${value.estimated ? "~" : ""}${format(used)} / ${format(ceiling)}`;
}

function renderLog(session) {
  const wasBottom = follow;
  thinkingRenderer.begin();
  usedEntryViews = new Set();
  if (!session) {
    const empty = document.createElement("div");
    empty.className = "chat-empty";
    empty.textContent = "Choose a session to start.";
    finishLogRender([empty]);
    return;
  }
  const entries = buildEntries(session);
  if (!entries.length) {
    const empty = document.createElement("div");
    empty.className = "chat-empty";
    empty.textContent = session.runnable ? "Send a task to start the loop." : session.not_runnable_reason;
    finishLogRender([empty]);
    return;
  }
  const nodes = [];
  const end = Math.max(0, entries.length - page * 100);
  const start = Math.max(0, end - 300);
  if (start > 0) {
    earlierButton.textContent = `earlier: ${start} entries`;
    nodes.push(earlierButton);
  }
  for (const entry of entries.slice(start, end)) nodes.push(renderEntry(session, entry));
  jumpButton.hidden = follow && page === 0;
  nodes.push(jumpButton);
  finishLogRender(nodes);
  requestAnimationFrame(() => {
    if (wasBottom && page === 0) log.scrollTop = log.scrollHeight;
    jumpButton.hidden = follow && page === 0;
  });
}

function finishLogRender(nodes) {
  reconcileChildren(log, nodes);
  thinkingRenderer.end();
  for (const key of entryViews.keys()) if (!usedEntryViews.has(key)) entryViews.delete(key);
}

function reconcileChildren(parent, nodes) {
  for (let index = 0; index < nodes.length; index++) {
    if (parent.children[index] !== nodes[index])
      parent.insertBefore(nodes[index], parent.children[index] || null);
  }
  while (parent.children.length > nodes.length) parent.lastElementChild.remove();
}

function buildEntries(session) {
  const entries = [];
  const turns = new Map();
  const calls = new Map();
  const decisions = new Map();
  const userMessageIDs = new Set();
  for (const event of session.timeline || []) {
    const data = event.data || {};
    const turnKey = `${event.run_id}:${data.turn}`;
    if (event.type === "message.appended" && data.message?.role === "user") {
      entries.push({ type: "user", key: `message:${data.message.id}`, text: data.message.content });
      userMessageIDs.add(data.message.id);
    } else if (event.type === "model.request") {
      const entry = { type: "agent", key: `turn:${turnKey}`, text: "", reasoning: "", reasoningTokens: 0, thinkingStartedMS: 0, thinkingEndedMS: 0, thinkingMS: null, done: false };
      turns.set(turnKey, entry);
      entries.push(entry);
    } else if (event.type === "model.delta") {
      let entry = turns.get(turnKey);
      if (!entry && activeModelTurn(session, event)) {
        entry = { type: "agent", key: `turn:${turnKey}`, text: "", reasoning: "", reasoningTokens: 0, thinkingStartedMS: 0, thinkingEndedMS: 0, thinkingMS: null, done: false };
        turns.set(turnKey, entry);
        entries.push(entry);
      }
      if (entry) {
        if (data.kind === "reasoning") {
          if (!entry.thinkingStartedMS) entry.thinkingStartedMS = eventTime(event);
          entry.reasoning += data.text || "";
        } else {
          if (entry.thinkingStartedMS && !entry.thinkingEndedMS) entry.thinkingEndedMS = eventTime(event);
          if (data.kind === "content") entry.text += data.text || "";
        }
      }
    } else if (event.type === "model.response") {
      let entry = turns.get(turnKey);
      if (!entry) {
        entry = { type: "agent", key: `turn:${turnKey}`, text: "", reasoning: "", reasoningTokens: 0, thinkingStartedMS: 0, thinkingEndedMS: 0, thinkingMS: null, done: false };
        turns.set(turnKey, entry);
        entries.push(entry);
      }
      entry.text = data.content || entry.text;
      entry.toolCallIDs = (data.tool_calls || []).map((call) => call.id);
      entry.reasoningTokens = data.reasoning_tokens || 0;
      entry.reasoningTokensEstimated = !!data.reasoning_tokens_estimated;
      if (entry.thinkingStartedMS && !entry.thinkingEndedMS) entry.thinkingEndedMS = eventTime(event);
      entry.thinkingMS = entry.thinkingStartedMS && entry.thinkingEndedMS
        ? Math.max(0, entry.thinkingEndedMS - entry.thinkingStartedMS)
        : entry.reasoningTokens > 0 ? data.duration_ms || null : null;
      entry.done = true;
    } else if (event.type === "tool.call") {
      const entry = { type: "tool", key: `tool:${data.call_id}`, callID: data.call_id, name: data.name, args: data.args || {}, result: null };
      calls.set(data.call_id, entry);
      entries.push(entry);
    } else if (event.type === "tool.result") {
      const entry = calls.get(data.call_id);
      if (entry) entry.result = data;
    } else if (event.type === "approval.decided") {
      decisions.set(data.call_id, data.decision);
    } else if (noticeTypes.has(event.type)) {
	  if (event.type === "run.stopped")
		for (const [key, turn] of turns) if (key.startsWith(`${event.run_id}:`)) turn.done = true;
      entries.push({ type: "notice", key: `event:${event.seq}`, event, decisions });
    }
  }
  for (const entry of entries) {
    if (entry.type !== "tool") continue;
    entry.content = (session.messages || []).find((message) => message.tool_call_id === entry.callID)?.content || entry.result?.preview || "";
  }
  // The live timeline is a bounded tail. Rebuild messages that aged out as one
  // ordered prefix; separating user and assistant fallbacks destroys turn order.
  const matchedMessages = hydrateAgentEntries(entries, session.messages || []);
  const callDetails = messageCallDetails(session.messages || []);
  const missing = [];
  for (let index = (session.messages || []).length - 1; index >= 0; index--) {
    const message = session.messages[index];
    if (message.role === "user") {
      if (userMessageIDs.has(message.id)) continue;
      missing.push({ type: "user", key: `message:${message.id}`, text: message.content });
    } else if (message.role === "assistant") {
      if (matchedMessages.has(message)) continue;
      if (message.content || message.reasoning) {
        const tokens = Math.ceil(Array.from(message.reasoning || "").length / 3.6);
        const rate = Number(session._timings?.predicted_per_second || 0);
        missing.push({
          type: "agent",
          key: `message:${message.id}`,
          text: message.content || "",
          reasoning: message.reasoning || "",
          reasoningTokens: 0,
          thinkingMS: tokens > 0 && rate > 0 ? (tokens / rate) * 1000 : null,
          thinkingEstimated: tokens > 0,
          done: true,
        });
      }
    } else if (message.role === "tool" && !calls.has(message.tool_call_id)) {
      const detail = callDetails.get(message.tool_call_id) || {};
      const content = message.content || "";
      missing.push({
        type: "tool",
        key: `tool-message:${message.id}`,
        callID: message.tool_call_id,
        name: message.name || detail.name || "tool",
        args: detail.args || {},
        content,
        result: { ok: typeof message.ok === "boolean" ? message.ok : null, ms: null, preview: content },
      });
    }
  }
  entries.unshift(...missing.reverse());
  const active = [...entries].reverse().find((entry) => entry.type === "agent" && !entry.done);
  if (session.run.partial && active && session.run.partial.length > active.text.length) active.text = session.run.partial;
  return groupResponses(entries);
}

function groupResponses(entries) {
  const grouped = [];
  let response = null;
  let boundary = "orphan";
  for (const entry of entries) {
    if (entry.type === "user") {
      grouped.push(entry);
      boundary = entry.key;
      response = null;
      continue;
    }
    if (!response) {
      response = { type: "response", key: `response:${boundary}`, items: [] };
      grouped.push(response);
    }
    response.items.push(entry);
  }
  return grouped;
}

function eventTime(event) {
  const value = Date.parse(event.ts || "");
  return Number.isFinite(value) ? value : 0;
}

function activeModelTurn(session, event) {
  const status = session.run?.status || "idle";
  return status !== "idle" && status !== "replay" && modelTurnKey(event) === `${session.run.run_id}:${session.run.turn}`;
}

function messageCallDetails(messages) {
  const details = new Map();
  for (const message of messages) {
    for (const call of message.tool_calls || []) {
      let args = call.function?.arguments || {};
      if (typeof args === "string") {
        try { args = JSON.parse(args); }
        catch { args = { arguments: args }; }
      }
      details.set(call.id, { name: call.function?.name || "tool", args });
    }
  }
  return details;
}

const noticeTypes = new Set([
  "run.stopped",
  "run.queued",
  "message.queued",
  "compaction",
  "workspace.conflict",
  "approval.required",
  "memory.noted",
]);

function renderEntry(session, entry) {
  if (entry.type === "notice") return renderNotice(session, entry);
  if (entry.type === "response") return renderResponse(session, entry);
  let view = entryViews.get(entry.key);
  if (!view) {
    const row = document.createElement("section");
    row.tabIndex = 0;
    const content = document.createElement("div");
    content.className = "chat-content";
    row.append(speaker(entry.type === "user" ? "you" : "agent"), content);
    view = { row, content, text: "" };
    entryViews.set(entry.key, view);
  }
  usedEntryViews.add(entry.key);
  view.row.className = `chat-entry ${entry.type === "user" ? "chat-user" : entry.type === "tool" ? "tool-entry" : "chat-agent"}`;
  const content = view.content;
  if (entry.type === "user") {
    if (view.text !== entry.text) content.textContent = entry.text;
    view.text = entry.text;
  }
  else if (entry.type === "tool") content.append(toolTick(entry));
  else {
    const nodes = [];
    const tokens = entry.reasoningTokens || Math.ceil(Array.from(entry.reasoning || "").length / 3.6);
    if (tokens > 0 || !entry.done) nodes.push(thinking(entry, tokens));
    if (entry.text) {
      const answer = document.createElement("div");
      renderMarkdown(answer, entry.text);
      nodes.push(answer);
    }
    if (!entry.done) {
      const caret = document.createElement("span");
      caret.className = "stream-caret";
      nodes.push(caret);
    }
    reconcileChildren(content, nodes);
  }
  return view.row;
}

function renderResponse(session, entry) {
  let view = entryViews.get(entry.key);
  if (!view) {
    const row = document.createElement("section");
    row.className = "chat-entry chat-agent chat-response";
    row.tabIndex = 0;
    const content = document.createElement("div");
    content.className = "chat-content chat-response-content";
    row.append(speaker("agent"), content);
    view = { row, content, items: new Map() };
    entryViews.set(entry.key, view);
  }
  usedEntryViews.add(entry.key);
  const nodes = [];
  const usedItems = new Set();
  for (const item of entry.items) {
    usedItems.add(item.key);
    if (item.type === "notice") {
      const notice = noticeContent(session, item);
      notice.classList.add("chat-response-notice");
      nodes.push(notice);
      continue;
    }
    let itemView = view.items.get(item.key);
    if (!itemView) {
      const step = document.createElement("div");
      step.className = `chat-response-step ${item.type === "tool" ? "chat-response-tool" : ""}`;
      itemView = { step, answer: null, caret: null, answerText: "" };
      view.items.set(item.key, itemView);
    }
    const stepNodes = [];
    if (item.type === "agent") {
      const tokens = item.reasoningTokens || Math.ceil(Array.from(item.reasoning || "").length / 3.6);
      if (tokens > 0 || !item.done) stepNodes.push(thinking(item, tokens));
      if (item.text) {
        if (!itemView.answer) {
          itemView.answer = document.createElement("div");
          itemView.answer.className = "chat-response-answer";
        }
        if (itemView.answerText !== item.text) renderMarkdown(itemView.answer, item.text);
        itemView.answerText = item.text;
        stepNodes.push(itemView.answer);
      }
      if (!item.done) {
        if (!itemView.caret) {
          itemView.caret = document.createElement("span");
          itemView.caret.className = "stream-caret";
        }
        stepNodes.push(itemView.caret);
      }
    } else if (item.type === "tool") {
      stepNodes.push(toolTick(item));
    }
    reconcileChildren(itemView.step, stepNodes);
    nodes.push(itemView.step);
  }
  reconcileChildren(view.content, nodes);
  for (const key of view.items.keys()) if (!usedItems.has(key)) view.items.delete(key);
  return view.row;
}

function speaker(name) {
  const node = document.createElement("div");
  node.className = "chat-speaker";
  if (name === "agent") {
    const image = document.createElement("img");
    image.src = "/static/assets/agent.svg";
    image.alt = "";
    node.append(image);
  }
  const label = document.createElement("span");
  label.textContent = name;
  node.append(label);
  return node;
}

function thinking(entry, tokens) {
  return thinkingRenderer.render(entry, tokens);
}

function toolTick(entry) {
  const root = document.createElement("div");
  const button = document.createElement("button");
  button.type = "button";
  button.className = "tool-tick";
  const open = expanded.has(entry.key);
  button.setAttribute("aria-expanded", String(open));
  const state = entry.result && typeof entry.result.ok === "boolean" ? (entry.result.ok ? "ok" : "error") : "";
  button.innerHTML = '<span class="tool-name"></span><span class="tool-key"></span><span class="tool-state"></span><span class="tool-ms"></span>';
  button.children[0].textContent = `${open ? "▾" : "▸"} ${entry.name}`;
  button.children[1].textContent = keyArgument(entry.args);
  button.children[2].textContent = state;
  button.children[2].className = `tool-state ${state === "error" ? "error" : ""}`;
  button.children[3].textContent = entry.result && entry.result.ms !== null && entry.result.ms !== undefined ? `${entry.result.ms} ms` : "";
  button.onclick = () => {
    expanded.has(entry.key) ? expanded.delete(entry.key) : expanded.add(entry.key);
    render();
  };
  root.append(button);
  if (expanded.has(entry.key)) {
    const pre = document.createElement("pre");
    pre.className = "tool-detail";
    pre.textContent = `arguments\n${JSON.stringify(entry.args, null, 2)}\n\nresult\n${capResult(entry.content)}`;
    root.append(pre);
  }
  return root;
}

function formatThoughtSeconds(milliseconds) {
  if (!Number.isFinite(milliseconds)) return "";
  const seconds = Math.max(0, milliseconds) / 1000;
  return (seconds < 10 ? seconds.toFixed(1) : seconds.toFixed(0)).replace(/\.0$/, "");
}

function renderNotice(session, entry) {
  const row = document.createElement("div");
  row.className = "chat-entry chat-notice-row";
  const content = noticeContent(session, entry);
  if (content.classList.contains("alarm")) row.classList.add("alarm");
  row.append(content);
  return row;
}

function noticeContent(session, entry) {
  const event = entry.event;
  const data = event.data || {};
  const content = document.createElement("div");
  content.className = "chat-content";
  if (event.type === "run.stopped") {
    const reason = (data.reason || "").replaceAll("_", " ");
    content.textContent = `stopped: ${reason}${data.reason === "turn_ceiling" ? ` (${data.turns || session.run.max_turns})` : data.detail ? `, ${data.detail}` : ""}`;
    if (data.reason !== "done") content.classList.add("alarm");
  } else if (event.type === "run.queued") content.textContent = `waiting for a slot (position ${data.position})`;
  else if (event.type === "message.queued") content.textContent = `queued (${data.position})`;
  else if (event.type === "compaction") content.textContent = `compacted ${signed((data.after || 0) - (data.before || 0))} tokens`;
  else if (event.type === "workspace.conflict") {
    content.textContent = `conflict: ${data.path} written by ${data.other_label} ${data.age_s} s ago`;
    content.classList.add("alarm");
  } else if (event.type === "memory.noted") content.textContent = "noted for next session";
  else if (event.type === "approval.required") {
    content.className += " chat-approval";
    const boundaryEscape = typeof data.boundary_escape === "boolean"
      ? data.boundary_escape
      : data.name?.endsWith(".operator_override");
    const shellPolicy = !boundaryEscape && data.name === "shell";
    if (boundaryEscape) content.classList.add("alarm");
    content.append(document.createTextNode(boundaryEscape
	  ? `Privilege escalation: ${data.args?.reason || "service identity could not run operation"}. Run once as your Windows account? ${keyArgument(data.args || {})}`
      : shellPolicy
        ? `Run shell command? ${keyArgument(data.args || {})}`
        : `Policy confirmation: ${data.name} ${keyArgument(data.args || {})}`));
    const decision = entry.decisions.get(data.call_id);
    if (decision) content.append(document.createTextNode(` ${decision}`));
    else if (!store.replay) {
      for (const choice of boundaryEscape
        ? [["approve", "Run once as operator"], ["deny", "Keep denied"]]
        : [["approve", "Approve"], ["deny", "Deny"]]) {
        const [value, label] = choice;
        const button = document.createElement("button");
        button.type = "button";
        button.textContent = label;
        button.onclick = () => api("/api/approve", { session_id: session.id, call_id: data.call_id, decision: value });
        content.append(button);
      }
    }
  }
  return content;
}

function renderComposer(session) {
  const running = busy(session);
  send.textContent = running ? "Stop" : "Send";
  send.disabled = !session || !!store.replay;
  input.disabled = !session || !!store.replay;
  if (store.replay) input.placeholder = "Replay";
  else if (session?.run.status === "paused") input.placeholder = "paused — waiting for approval";
  else input.placeholder = "Send a task";
  const queued = session?.queued_messages || 0;
  const message = localNotice || (session && !session.runnable ? session.not_runnable_reason : queued ? `queued (${queued})` : session?.run.status === "paused" ? "paused — waiting for approval" : "");
  notice.textContent = message;
  notice.className = `chat-notice ${localAlarm || (session && !session.runnable) ? "alarm" : ""}`;
}

async function submit() {
  const session = store.sessions[bound];
  if (!session || store.replay) return;
  if (busy(session) && (store.config.run?.queue_depth || 0) === 0) {
    localNotice = "Run in progress";
    localAlarm = true;
    return renderComposer(session);
  }
  const text = input.value.trim();
  if (!text) return;
  try {
    const result = await api("/api/message", { session_id: session.id, text });
    input.value = "";
    resize();
    localNotice = result.queued ? `queued (${result.position})` : "";
    localAlarm = false;
  } catch (error) {
    localNotice = error.message;
    localAlarm = true;
  }
  renderComposer(session);
}

async function stopRun() {
  if (!bound || store.replay) return;
  try {
    await api("/api/stop", { session_id: bound });
    localNotice = "";
    localAlarm = false;
  } catch (error) {
    localNotice = error.message;
    localAlarm = true;
  }
  renderComposer(store.sessions[bound]);
}

send.onclick = () => (busy(store.sessions[bound]) ? stopRun() : submit());
stop.onclick = stopRun;
input.addEventListener("input", resize);
input.addEventListener("keydown", (event) => {
  if (event.key === "Enter" && !event.shiftKey) {
    event.preventDefault();
    submit();
  } else if (event.key === "Escape") {
    input.value = "";
    resize();
  }
});
log.addEventListener("scroll", () => {
  follow = log.scrollHeight - log.clientHeight - log.scrollTop <= 24;
});
document.addEventListener("keydown", (event) => {
  if (event.ctrlKey && event.key === ".") {
    event.preventDefault();
    stopRun();
  } else if (event.key === "/" && !/INPUT|TEXTAREA|SELECT/.test(document.activeElement?.tagName || "")) {
    event.preventDefault();
    input.focus();
  }
});

function resize() {
  input.style.height = "auto";
  input.style.height = `${Math.min(input.scrollHeight, 120)}px`;
}
function busy(session) {
  return !!session && ["running", "queued", "paused", "stopping"].includes(session.run.status);
}
function keyArgument(args) {
  for (const key of ["path", "command", "pattern", "note"]) if (args[key] !== undefined) return String(args[key]);
  const first = Object.values(args)[0];
  return first === undefined ? "" : typeof first === "string" ? first : JSON.stringify(first);
}
function capResult(value) {
  const lines = String(value || "").split("\n");
  return lines.length <= 200 ? lines.join("\n") : [...lines.slice(0, 199), "[… open in timeline for the rest]"].join("\n");
}
function renderMarkdown(root, text) {
  const lines = String(text).split("\n");
  let index = 0;
  while (index < lines.length) {
    if (lines[index].startsWith("```")) {
      const code = [];
      index++;
      while (index < lines.length && !lines[index].startsWith("```")) code.push(lines[index++]);
      index++;
      const pre = document.createElement("pre");
      pre.textContent = code.join("\n");
      root.append(pre);
    } else if (/^[-*] /.test(lines[index])) {
      const list = document.createElement("ul");
      while (index < lines.length && /^[-*] /.test(lines[index])) {
        const item = document.createElement("li");
        inline(item, lines[index].slice(2));
        list.append(item);
        index++;
      }
      root.append(list);
    } else if (lines[index].trim()) {
      const paragraph = [];
      while (index < lines.length && lines[index].trim() && !lines[index].startsWith("```") && !/^[-*] /.test(lines[index])) paragraph.push(lines[index++]);
      const p = document.createElement("p");
      inline(p, paragraph.join("\n"));
      root.append(p);
    } else index++;
  }
}
function inline(root, text) {
  const pattern = /(`[^`]+`|\*\*[^*]+\*\*)/g;
  let at = 0;
  for (const match of text.matchAll(pattern)) {
    root.append(document.createTextNode(text.slice(at, match.index)));
    const value = match[0];
    const node = document.createElement(value.startsWith("`") ? "code" : "strong");
    node.textContent = value.startsWith("`") ? value.slice(1, -1) : value.slice(2, -2);
    root.append(node);
    at = match.index + value.length;
  }
  root.append(document.createTextNode(text.slice(at)));
}
function signed(value) {
  return `${value < 0 ? "−" : value > 0 ? "+" : "±"}${format(Math.abs(value))}`;
}
function format(value) {
  return Number(value || 0).toLocaleString("en-US");
}
