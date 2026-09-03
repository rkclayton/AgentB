import { api, store, subscribe } from "./bus.js";

const binding = document.getElementById("chat-binding");
const status = document.getElementById("chat-status");
const stop = document.getElementById("chat-stop");
const budget = document.getElementById("chat-budget");
const log = document.getElementById("chat-log");
const input = document.getElementById("chat-task");
const send = document.getElementById("chat-send");
const notice = document.getElementById("chat-notice");
const requested = new URLSearchParams(location.search).get("session");
const expanded = new Set();
let bound = requested || "";
let follow = true;
let page = 0;
let localNotice = "";
let localAlarm = false;
let frame = 0;

subscribe((_state, event) => {
  if (event.type === "snapshot") {
    if (!store.sessions[bound]) bound = requested && store.sessions[requested] ? requested : Object.keys(store.sessions)[0] || "";
  }
  schedule();
});

function schedule() {
  if (frame) return;
  frame = requestAnimationFrame(() => {
    frame = 0;
    render();
  });
}

function render() {
  const session = store.sessions[bound];
  renderBinding(session);
  renderHeader(session);
  renderBudget(session);
  renderLog(session);
  renderComposer(session);
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
  log.replaceChildren();
  if (!session) {
    const empty = document.createElement("div");
    empty.className = "chat-empty";
    empty.textContent = "Choose a session to start.";
    log.append(empty);
    return;
  }
  const entries = buildEntries(session);
  if (!entries.length) {
    const empty = document.createElement("div");
    empty.className = "chat-empty";
    empty.textContent = session.runnable ? "Send a task to start the loop." : session.not_runnable_reason;
    log.append(empty);
    return;
  }
  const end = Math.max(0, entries.length - page * 100);
  const start = Math.max(0, end - 300);
  if (start > 0) {
    const earlier = document.createElement("button");
    earlier.type = "button";
    earlier.className = "chat-earlier";
    earlier.textContent = `earlier: ${start} entries`;
    earlier.onclick = () => {
      page++;
      follow = false;
      renderLog(session);
    };
    log.append(earlier);
  }
  for (const entry of entries.slice(start, end)) log.append(renderEntry(session, entry));
  const jump = document.createElement("button");
  jump.type = "button";
  jump.className = "chat-jump";
  jump.textContent = "Jump to latest";
  jump.hidden = follow && page === 0;
  jump.onclick = () => {
    follow = true;
    page = 0;
    renderLog(session);
  };
  log.append(jump);
  requestAnimationFrame(() => {
    if (wasBottom && page === 0) log.scrollTop = log.scrollHeight;
    jump.hidden = follow && page === 0;
  });
}

function buildEntries(session) {
  const entries = [];
  const turns = new Map();
  const calls = new Map();
  const decisions = new Map();
  const messageIDs = new Set();
  for (const event of session.timeline || []) {
    const data = event.data || {};
    const turnKey = `${event.run_id}:${data.turn}`;
    if (event.type === "message.appended" && data.message?.role === "user") {
      entries.push({ type: "user", key: `message:${data.message.id}`, text: data.message.content });
      messageIDs.add(data.message.id);
    } else if (event.type === "message.appended" && data.message) {
      messageIDs.add(data.message.id);
    } else if (event.type === "model.request") {
      const entry = { type: "agent", key: `turn:${turnKey}`, text: "", reasoning: "", reasoningTokens: 0, done: false };
      turns.set(turnKey, entry);
      entries.push(entry);
    } else if (event.type === "model.delta") {
      const entry = turns.get(turnKey);
      if (entry) {
        if (data.kind === "content") entry.text += data.text || "";
        if (data.kind === "reasoning") entry.reasoning += data.text || "";
      }
    } else if (event.type === "model.response") {
      let entry = turns.get(turnKey);
      if (!entry) {
        entry = { type: "agent", key: `turn:${turnKey}`, text: "", reasoning: "", reasoningTokens: 0, done: false };
        turns.set(turnKey, entry);
        entries.push(entry);
      }
      entry.text = data.content || entry.text;
      entry.reasoningTokens = data.reasoning_tokens || 0;
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
  const representedText = new Map();
  for (const entry of entries) {
    if (entry.type === "agent" && entry.text)
      representedText.set(entry.text, (representedText.get(entry.text) || 0) + 1);
  }
  const callDetails = messageCallDetails(session.messages || []);
  const missing = [];
  for (let index = (session.messages || []).length - 1; index >= 0; index--) {
    const message = session.messages[index];
    if (messageIDs.has(message.id)) continue;
    if (message.role === "user") {
      missing.push({ type: "user", key: `message:${message.id}`, text: message.content });
    } else if (message.role === "assistant") {
      const count = representedText.get(message.content) || 0;
      if (message.content && count > 0) {
        representedText.set(message.content, count - 1);
        continue;
      }
      if ((message.tool_calls || []).some((call) => calls.has(call.id))) continue;
      if (message.content || message.reasoning)
        missing.push({ type: "agent", key: `message:${message.id}`, text: message.content || "", reasoning: message.reasoning || "", reasoningTokens: 0, done: true });
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
        result: { ok: !String(content).startsWith("error:"), ms: null, preview: content },
      });
    }
  }
  entries.unshift(...missing.reverse());
  const active = [...entries].reverse().find((entry) => entry.type === "agent" && !entry.done);
  if (session.run.partial && active && session.run.partial.length > active.text.length) active.text = session.run.partial;
  return entries;
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
  const row = document.createElement("section");
  row.className = `chat-entry ${entry.type === "user" ? "chat-user" : entry.type === "tool" ? "tool-entry" : "chat-agent"}`;
  row.tabIndex = 0;
  row.append(speaker(entry.type === "user" ? "you" : "agent"));
  const content = document.createElement("div");
  content.className = "chat-content";
  if (entry.type === "user") content.textContent = entry.text;
  else if (entry.type === "tool") content.append(toolTick(entry));
  else {
    const tokens = entry.reasoningTokens || Math.ceil((entry.reasoning || "").length / 3.6);
    if (tokens > 0 || (!entry.done && entry.reasoning)) content.append(thinking(entry, tokens));
    if (entry.text) renderMarkdown(content, entry.text);
    if (!entry.done) {
      const caret = document.createElement("span");
      caret.className = "stream-caret";
      content.append(caret);
    }
  }
  row.append(content);
  return row;
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
  const root = document.createElement("div");
  const button = document.createElement("button");
  button.type = "button";
  button.className = "thinking-line";
  button.textContent = `${entry.done ? "thinking" : "thinking…"}  ${format(tokens)} tokens`;
  button.onclick = () => {
    expanded.has(entry.key) ? expanded.delete(entry.key) : expanded.add(entry.key);
    render();
  };
  root.append(button);
  if (expanded.has(entry.key)) {
    const pre = document.createElement("pre");
    pre.className = "thinking-body";
    pre.textContent = entry.reasoning;
    root.append(pre);
  }
  return root;
}

function toolTick(entry) {
  const root = document.createElement("div");
  const button = document.createElement("button");
  button.type = "button";
  button.className = "tool-tick";
  const state = entry.result ? (entry.result.ok ? "ok" : "error") : "";
  button.innerHTML = '<span class="tool-name"></span><span class="tool-key"></span><span class="tool-state"></span><span class="tool-ms"></span>';
  button.children[0].textContent = `▸ ${entry.name}`;
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

function renderNotice(session, entry) {
  const event = entry.event;
  const data = event.data || {};
  const row = document.createElement("div");
  row.className = "chat-entry chat-notice-row";
  const content = document.createElement("div");
  content.className = "chat-content";
  if (event.type === "run.stopped") {
    const reason = (data.reason || "").replaceAll("_", " ");
    content.textContent = `stopped: ${reason}${data.reason === "turn_ceiling" ? ` (${data.turns || session.run.max_turns})` : data.detail ? `, ${data.detail}` : ""}`;
    if (data.reason !== "done") row.classList.add("alarm");
  } else if (event.type === "run.queued") content.textContent = `waiting for a slot (position ${data.position})`;
  else if (event.type === "message.queued") content.textContent = `queued (${data.position})`;
  else if (event.type === "compaction") content.textContent = `compacted ${signed((data.after || 0) - (data.before || 0))} tokens`;
  else if (event.type === "workspace.conflict") {
    content.textContent = `conflict: ${data.path} written by ${data.other_label} ${data.age_s} s ago`;
    row.classList.add("alarm");
  } else if (event.type === "memory.noted") content.textContent = "noted for next session";
  else if (event.type === "approval.required") {
    content.className += " chat-approval";
    content.append(document.createTextNode(`approval: ${data.name} ${keyArgument(data.args || {})}`));
    const decision = entry.decisions.get(data.call_id);
    if (decision) content.append(document.createTextNode(` ${decision}`));
    else if (!store.replay) {
      for (const value of ["Approve", "Deny"]) {
        const button = document.createElement("button");
        button.type = "button";
        button.textContent = value;
        button.onclick = () => api("/api/approve", { session_id: session.id, call_id: data.call_id, decision: value.toLowerCase() });
        content.append(button);
      }
    }
  }
  row.append(content);
  return row;
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
