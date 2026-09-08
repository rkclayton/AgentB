import { api, reduce, setSelection, store, subscribe } from "./bus.js";
import { chatRowText, closeConfirmText, firstUserLine, isRunning } from "./chat-lifecycle.js";
import { agentTabLayout } from "./agent-tabs.js";

const activeRunStates = new Set(["running", "queued", "stopping"]);
const agentKey = (agent) => String(agent?.name || "").trim().toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");

export function initShell(options = {}) {
  const root = document.getElementById("app-shell");
  if (!root) return null;
  const page = options.page || root.dataset.page || "console";
  root.replaceChildren();

  const left = node("div", "shell-left");
  const tabs = node("nav", "agent-tabs");
  tabs.setAttribute("aria-label", "Agents");
  left.append(tabs);

  const right = node("div", "shell-right");
  const pages = node("nav", "shell-pages");
  pages.setAttribute("aria-label", "Pages");
  for (const [id, label, path] of [["chat", "Chat", "/chat"], ["console", "Console", "/"], ["plan", "Plan", "/plan"]]) {
    const link = node("a", `shell-page ${page === id ? "selected" : ""}`);
    link.dataset.page = id;
    link.textContent = label;
    link.href = path;
    if (page === id) {
      link.setAttribute("aria-current", "page");
      link.onclick = (event) => event.preventDefault();
    }
    pages.append(link);
  }
  const settings = node("a", "shell-settings");
  settings.textContent = "⚙";
  settings.setAttribute("aria-label", "Settings");
  settings.title = "Settings";
  right.append(pages, settings);
  root.append(left, right);
  document.addEventListener("click", (event) => {
    if (!tabs.contains(event.target)) for (const menu of tabs.querySelectorAll(".shell-menu")) menu.hidden = true;
  });

  function report(message) {
    if (options.reportError) options.reportError(message);
    else {
      root.dataset.error = message;
    }
  }

  function sessionsFor(agentID, includeClosed = true) {
    if (agentID !== "agent_b") return [];
    return Object.values(store.sessions)
      .filter((session) => includeClosed || !session.closed)
      .sort((a, b) => Date.parse(b.created_at || 0) - Date.parse(a.created_at || 0));
  }

  function agentName(agentID) {
    const selected = store.sessions[store.selection.session_id];
    if (agentID === "agent_b") {
      if (selected?.agent_name) return selected.agent_name;
    }
	const selectedAgentID = selected?.agent_id || agentKey(store.config.agents?.[0]);
	const configured = (store.config.agents || []).find((agent) => agentKey(agent) === selectedAgentID) || store.config.agents?.[0];
	const profileID = configured?.[agentID.replace("agent_", "")];
    const profile = store.servers.find((item) => item.id === profileID);
    return profile?.label || profileID || agentID;
  }

  function agentState(agentID) {
    const sessions = sessionsFor(agentID, false);
    if (sessions.some((item) => item.pending_approval || item.pending_repo_policy || item.run?.status === "paused")) return "waiting";
    if (sessions.some((item) => activeRunStates.has(item.run?.status))) return "running";
    return "idle";
  }

  function renderTabs() {
    tabs.replaceChildren();
    const agents = ["agent_b"];
	const selected = store.sessions[store.selection.session_id];
	const configured = (store.config.agents || []).find((agent) => agentKey(agent) === selected?.agent_id) || store.config.agents?.[0];
	if (configured?.c) agents.push("agent_c");
	if (configured?.d) agents.push("agent_d");
    for (const agentID of agents) {
      const wrap = node("div", "agent-tab-wrap");
      wrap.dataset.agent = agentID;
      const tab = button("", `${agentID} · ${agentName(agentID)}`, `agent-tab ${store.selection.agent_id === agentID ? "selected" : ""}`);
      const glyphState = agentState(agentID);
      tab.dataset.agent = agentID;
      tab.innerHTML = `<span class="agent-state ${glyphState}" aria-hidden="true">${glyphState === "waiting" ? "!" : glyphState === "running" ? "●" : "○"}</span>${agentID === "agent_b" ? '<img class="agent-tab-robot" src="/static/assets/agent.svg" alt="">' : ""}<span>${escapeHTML(agentID)}</span>`;
      tab.onclick = () => {
        const current = store.selection.session_id;
        const owned = sessionsFor(agentID, false);
        setSelection(agentID, owned.some((item) => item.id === current) ? current : owned[0]?.id || "");
      };
      const menu = node("div", "shell-menu agent-chat-menu");
      menu.hidden = true;
      tab.oncontextmenu = (event) => {
        event.preventDefault();
        for (const other of tabs.querySelectorAll(".shell-menu")) if (other !== menu) other.hidden = true;
        renderAgentMenu(menu, agentID);
        menu.hidden = false;
      };
      wrap.append(tab, menu);
      tabs.append(wrap);
    }
    const overflowWrap = node("div", "agent-overflow-wrap");
    const overflowButton = button("", "More agents", "agent-overflow");
    const overflowMenu = node("div", "shell-menu agent-overflow-menu");
    overflowMenu.hidden = true;
    overflowButton.setAttribute("aria-haspopup", "menu");
    overflowButton.onclick = (event) => {
      event.stopPropagation();
      for (const other of tabs.querySelectorAll(".shell-menu")) if (other !== overflowMenu) other.hidden = true;
      renderOverflowMenu(overflowMenu);
      overflowMenu.hidden = !overflowMenu.hidden;
    };
    overflowWrap.append(overflowButton, overflowMenu);
    tabs.append(overflowWrap);
    queueMicrotask(layoutTabs);
  }

  function layoutTabs() {
    const entries = [...tabs.querySelectorAll(".agent-tab-wrap")];
    const overflowWrap = tabs.querySelector(".agent-overflow-wrap");
    const overflowButton = tabs.querySelector(".agent-overflow");
    if (!overflowWrap || !overflowButton) return;
    for (const entry of entries) entry.hidden = false;
    overflowWrap.hidden = true;
    const layout = agentTabLayout(entries.length, tabs.clientWidth);
    const visible = new Set(Array.from({ length: layout.visible }, (_, index) => index));
    const selectedIndex = entries.findIndex((entry) => entry.dataset.agent === store.selection.agent_id);
    if (layout.hidden && selectedIndex >= layout.visible) {
      visible.delete(layout.visible - 1);
      visible.add(selectedIndex);
    }
    entries.forEach((entry, index) => { entry.hidden = !visible.has(index); });
    const hidden = entries.filter((entry) => entry.hidden);
    overflowWrap.hidden = hidden.length === 0;
    overflowButton.textContent = `+${hidden.length}`;
    overflowButton.setAttribute("aria-label", `${hidden.length} more agents`);
  }

  function renderOverflowMenu(menu) {
    menu.replaceChildren();
    for (const entry of tabs.querySelectorAll(".agent-tab-wrap[hidden]")) {
      const source = entry.querySelector(".agent-tab");
      const choice = button(source?.textContent?.trim() || entry.dataset.agent, `Select ${entry.dataset.agent}`, "shell-new-choice");
      choice.onclick = () => { source?.click(); menu.hidden = true; };
      menu.append(choice);
    }
  }

  function renderAgentMenu(menu, agentID) {
    const sessions = sessionsFor(agentID, true);
    menu.replaceChildren();
    const create = button("New chat…", `New chat with ${agentID}`, "shell-new-choice");
    create.disabled = store.replay || agentID !== "agent_b" || !(store.config.agents || []).length;
    create.onclick = () => void showNewChatMenu(menu, agentID);
    menu.append(create);
    if (!sessions.length) {
      const empty = node("span", "shell-menu-empty");
      empty.textContent = "No chats";
      menu.append(empty);
      return;
    }
    for (const session of sessions) {
      const row = node("div", `agent-chat-row ${session.closed ? "closed" : "open"}`);
      const summary = node("span", "agent-chat-summary");
      summary.textContent = `${chatRowText(session)}${session.closed ? " · closed" : ""}`;
      summary.title = firstUserLine(session);
      const open = button("Open", `Open ${firstUserLine(session)}`, "agent-chat-open");
      open.disabled = session.closed;
      open.onclick = () => { setSelection(agentID, session.id); menu.hidden = true; };
      const rename = button("Rename", `Rename ${firstUserLine(session)}`, "agent-chat-rename");
      rename.onclick = () => showRename(row, session, menu, agentID);
      const close = button("Close", `Close ${firstUserLine(session)}`, "agent-chat-close");
      close.disabled = session.closed || store.replay;
      close.onclick = () => void closeChat(session, menu, agentID);
      row.append(summary, open, rename, close);
      menu.append(row);
    }
  }

  function showRename(row, session, menu, agentID) {
    const editor = node("form", "agent-chat-rename-form");
    const input = document.createElement("input");
    input.value = session.label || firstUserLine(session);
    input.setAttribute("aria-label", "Chat name");
    const save = button("Save", "Save chat name", "agent-chat-rename-save");
    save.type = "submit";
    editor.append(input, save);
    editor.onsubmit = async (event) => {
      event.preventDefault();
      const label = input.value.trim();
      if (!label) return;
      try {
        await api(`/api/sessions/${encodeURIComponent(session.id)}`, { label });
        reduce({ type: "snapshot", data: await api("/api/state", undefined, "GET") });
        renderAgentMenu(menu, agentID);
      } catch (error) { report(error.message); }
    };
    row.replaceChildren(editor);
    input.focus();
    input.select();
  }

  async function closeChat(session, menu, agentID) {
    if (isRunning(session)) return report("This chat has a running run. Stop it before closing the chat.");
    if (!window.confirm(closeConfirmText(session))) return;
    try {
      await api(`/api/sessions/${encodeURIComponent(session.id)}`, undefined, "DELETE");
      reduce({ type: "snapshot", data: await api("/api/state", undefined, "GET") });
      renderAgentMenu(menu, agentID);
    } catch (error) { report(error.message); }
  }

  async function showNewChatMenu(menu, agentID = "agent_b") {
    if (store.replay) return;
    try {
      const choices = await api("/api/pick-folder", undefined, "GET");
      menu.replaceChildren();
      const addChoice = (label, path) => {
        const choice = button(label, path, "shell-new-choice");
        choice.onclick = () => { menu.hidden = true; void createChat(path, agentID); };
        menu.append(choice);
      };
      addChoice(`Default · ${choices.default}`, choices.default);
      for (const item of (choices.recent || []).filter((item) => item.dir && item.dir.toLowerCase() !== String(choices.default).toLowerCase()).slice(0, 6)) addChoice(item.dir, item.dir);
      const browse = button("Browse…", "Browse for workspace", "shell-new-choice");
      browse.onclick = async () => {
        menu.hidden = true;
        try {
          const picked = await api("/api/pick-folder", { default: choices.default });
          await createChat(picked.workspace_dir, agentID);
        } catch (error) { if (!String(error.message).includes("canceled")) report(error.message); }
      };
      menu.append(browse);
      menu.hidden = false;
    } catch (error) { report(error.message); }
  }

  async function createChat(workspace, agentID = "agent_b") {
    const source = store.sessions[store.selection.session_id] || Object.values(store.sessions).sort((a, b) => Date.parse(b.created_at || 0) - Date.parse(a.created_at || 0))[0];
    try {
      const configuredID = agentKey(store.config.agents?.[0]);
      const body = source ? { source_session_id: source.id, workspace } : { agent_id: configuredID, workspace };
      const result = await api("/api/sessions", body);
      reduce({ type: "snapshot", data: await api("/api/state", undefined, "GET") });
      setSelection(agentID, result.session.id);
    } catch (error) { report(error.message); }
  }

  function render() {
    const session = store.sessions[store.selection.session_id];
    renderTabs();
    const query = new URLSearchParams();
    if (session) query.set("session", session.id);
    const suffix = query.size ? `?${query}` : "";
    for (const link of pages.children) link.href = link.dataset.page === "console" ? `/${suffix}` : `/${link.dataset.page}${suffix}`;
    settings.href = `/${suffix}#settings/servers`;
    if (page === "chat") history.replaceState(null, "", `/chat${suffix}`);
  }

  subscribe((_state, event) => {
    render();
  });
  if (typeof ResizeObserver !== "undefined") new ResizeObserver(layoutTabs).observe(tabs);
  else window.addEventListener("resize", layoutTabs);
  return {
    render,
    report,
    newChat() {
      const menu = tabs.querySelector('.agent-tab-wrap[data-agent="agent_b"] .shell-menu');
      if (menu) void showNewChatMenu(menu, "agent_b");
    },
  };
}

function node(tag, className) {
  const value = document.createElement(tag);
  value.className = className;
  return value;
}
function button(text, title, className) {
  const value = node("button", className);
  value.type = "button";
  value.textContent = text;
  value.title = title;
  return value;
}
function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[character]);
}
