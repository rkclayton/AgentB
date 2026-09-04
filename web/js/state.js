import { store } from "./bus.js";

const states = new Map();
let rendered = "";
const list = document.getElementById("state-list");
const count = document.getElementById("state-count");

function view(id) {
  if (!states.has(id)) states.set(id, { expanded: new Set(), scroll: 0, follow: true });
  return states.get(id);
}

function save() {
  if (!rendered) return;
  const state = view(rendered);
  state.scroll = list.scrollTop;
  state.follow = list.scrollHeight - list.clientHeight - list.scrollTop <= 24;
}

export function renderState() {
  save();
  const session = store.sessions[store.active];
  rendered = store.active;
  list.replaceChildren();
  if (!session) {
    count.textContent = "";
    return;
  }
  const state = view(session.id);
  const messages = session.messages || [];
  count.textContent = String(messages.length);
  if (!messages.length) {
    const empty = document.createElement("div");
    empty.className = "panel-empty";
    empty.textContent = "—";
    list.append(empty);
  }
  for (const message of messages) list.append(messageRow(session, message, state));
  const jump = document.createElement("button");
  jump.type = "button";
  jump.className = "jump-latest";
  jump.textContent = "Latest";
  jump.hidden = true;
  jump.onclick = () => {
    state.follow = true;
    renderState();
  };
  list.append(jump);
  requestAnimationFrame(() => {
    list.scrollTop = state.follow ? list.scrollHeight : state.scroll;
    jump.hidden = state.follow;
  });
}

function messageRow(session, message, state) {
  const row = document.createElement("div");
  row.className = `state-row ${message.elided ? "elided" : ""} ${state.expanded.has(message.id) ? "expanded" : ""}`;
  const head = document.createElement("button");
  head.type = "button";
  head.className = "state-head";
  const preview = message.tool_calls?.length
    ? `${friendly(message.tool_calls[0].name)}  ${message.tool_calls[0].arguments.slice(0, 80)}`
    : (message.content || message.reasoning || "").replaceAll("\n", " ");
  head.innerHTML = '<span class="state-role"></span><span class="state-preview"></span><span class="state-tokens number"></span>';
  head.children[0].textContent = role(message);
  head.children[1].textContent = preview || "—";
  head.children[2].textContent = `${message.estimated ? "~" : ""}${formatNumber(message.tokens || 0)}`;
  head.onclick = () => {
    state.expanded.has(message.id) ? state.expanded.delete(message.id) : state.expanded.add(message.id);
    renderState();
  };
  row.append(head);

  const expansion = document.createElement("div");
  expansion.className = "row-expansion";
  if (message.reasoning) {
    const thinking = document.createElement("details");
    const summary = document.createElement("summary");
    summary.textContent = `Thinking · ${formatNumber(reasoningTokens(session, message))}`;
    const pre = document.createElement("pre");
    pre.textContent = message.reasoning;
    thinking.append(summary, pre);
    expansion.append(thinking);
  }
  if (message.content) {
    const pre = document.createElement("pre");
    pre.textContent = message.content;
    expansion.append(pre);
  }
  row.append(expansion);
  return row;
}

function role(message) {
  if (message.category === "summary") return "Summary";
  return { user: "You", assistant: "Agent", tool: "Tool", system: "System" }[message.role] || friendly(message.role);
}

function reasoningTokens(session, message) {
  const event = [...(session.timeline || [])].reverse().find(
    (item) => item.type === "model.response" && item.data?.turn === message.turn,
  );
  return event?.data?.reasoning_tokens || Math.ceil((message.reasoning || "").length / 3.6);
}

function friendly(value) {
  const text = String(value || "").replaceAll("_", " ");
  return text ? text[0].toUpperCase() + text.slice(1) : "";
}

function formatNumber(value) {
  return Number(value || 0).toLocaleString("en-US");
}
