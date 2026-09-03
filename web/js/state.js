import { store } from "./bus.js";

const categories = [
  "system",
  "memory",
  "tools",
  "history",
  "files",
  "results",
  "summary",
];
const states = new Map();
let rendered = "";
const list = document.getElementById("state-list"),
  filters = document.getElementById("state-filters"),
  count = document.getElementById("state-count");

function view(id) {
  if (!states.has(id))
    states.set(id, {
      expanded: new Set(),
      filters: new Set(categories),
      scroll: 0,
      follow: true,
    });
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
  filters.replaceChildren();
  if (!session) {
    count.textContent = "";
    return;
  }
  const state = view(session.id);
  count.textContent = `${session.messages.length} messages`;
  for (const category of categories) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = state.filters.has(category) ? "selected" : "";
    button.textContent = category;
    button.onclick = () => {
      state.filters.has(category)
        ? state.filters.delete(category)
        : state.filters.add(category);
      renderState();
    };
    filters.append(button);
  }
  const rows = [...session.messages];
  if (state.filters.has("tools")) {
    const tokens = session.budget?.categories?.tools || 0;
    rows.unshift({
      id: "__schemas",
      role: "schemas",
      category: "tools",
      content: `Enabled schemas: ${session.tools
        .filter((tool) => tool.enabled)
        .map((tool) => tool.name)
        .join(", ")}`,
      tokens,
      estimated: (session.budget?.estimated_categories || []).includes("tools"),
    });
  }
  rows
    .filter((message) => state.filters.has(message.category))
    .forEach((message, index) =>
      list.append(messageRow(session, message, index, state)),
    );
  const jump = jumpButton(state, renderState);
  list.append(jump);
  requestAnimationFrame(() => {
    if (state.follow) list.scrollTop = list.scrollHeight;
    else list.scrollTop = state.scroll;
    jump.hidden = state.follow;
  });
}
function messageRow(session, message, index, state) {
  const row = document.createElement("div");
  row.className = `state-row ${message.elided ? "elided" : ""} ${state.expanded.has(message.id) ? "expanded" : ""}`;
  const head = document.createElement("button");
  head.type = "button";
  head.className = "state-head";
  const preview = message.tool_calls?.length
    ? `⚙ ${message.tool_calls[0].name} ${message.tool_calls[0].arguments.slice(0, 60)}`
    : (message.content || message.reasoning || "").replaceAll("\n", " ");
  head.innerHTML = `<span class="state-index number"></span><span class="state-role"></span><span class="state-tokens number"></span><span class="state-preview"></span>`;
  head.children[0].textContent = String(index + 1);
  head.children[1].textContent = message.role;
  head.children[2].textContent = `${message.estimated ? "~" : ""}${formatNumber(message.tokens || 0)}`;
  head.children[3].textContent = preview;
  head.onclick = () => {
    state.expanded.has(message.id)
      ? state.expanded.delete(message.id)
      : state.expanded.add(message.id);
    renderState();
  };
  row.append(head);
  const expansion = document.createElement("div");
  expansion.className = "row-expansion";
  const category = document.createElement("span");
  category.className = "category-chip";
  category.textContent = message.category;
  expansion.append(category);
  if (message.reasoning) {
    const thinking = document.createElement("details");
    const summary = document.createElement("summary");
    summary.textContent = `thinking  ${formatNumber(reasoningTokens(session, message))} tokens`;
    const pre = document.createElement("pre");
    pre.textContent = message.reasoning;
    thinking.append(summary, pre);
    expansion.append(thinking);
  }
  const pre = document.createElement("pre");
  pre.textContent = message.content || "";
  expansion.append(pre);
  row.append(expansion);
  return row;
}
function reasoningTokens(session, message) {
  const event = [...(session.timeline || [])]
    .reverse()
    .find(
      (item) =>
        item.type === "model.response" && item.data?.turn === message.turn,
    );
  return (
    event?.data?.reasoning_tokens ||
    Math.ceil((message.reasoning || "").length / 3.6)
  );
}
function jumpButton(state, render) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "jump-latest";
  button.textContent = "Jump to latest";
  button.hidden = true;
  button.onclick = () => {
    state.follow = true;
    render();
  };
  return button;
}
function formatNumber(value) {
  return Number(value || 0).toLocaleString("en-US");
}
