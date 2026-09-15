import { api, reduce, setSelection, store, subscribe } from "./bus.js";
import { chatRowText, firstUserLine, isRunning, sessionTitle } from "./chat-lifecycle.js";
import { installUIErrorRelay } from "./ui-error-relay.js";
import { requestNavigation } from "./navigation-guard.js";
import { beginNavigation } from "./navigation-telemetry.js";

const activeRunStates = new Set(["running", "queued", "stopping"]);
const agentKey = (agent) => String(agent?.name || "").trim().toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
const agentSideKey = (agentID) => `agentb.side.${agentID}`;

function rememberedAgentSide(agentID) {
  try {
    const value = sessionStorage.getItem(agentSideKey(agentID));
    if (value === "chat" || value === "console") return value;
  } catch {}
  return "chat";
}

function rememberAgentSide(agentID, side) {
  if (side !== "chat" && side !== "console") return;
  try { sessionStorage.setItem(agentSideKey(agentID), side); } catch {}
}

export function initShell(options = {}) {
	installUIErrorRelay({ token: () => store.mutation_token, sessionID: () => store.active });
  const root = document.getElementById("app-shell");
  if (!root) return null;
  let page = options.page || root.dataset.page || "console";
  root.replaceChildren();

  const left = node("div", "shell-left");
  const newChatButton = button("+", "New chat with agent_b", "agent-tab-new");
  const newChatMenu = node("div", "shell-menu shell-new-menu");
  newChatMenu.hidden = true;
  const tabs = node("nav", "agent-tabs");
  tabs.setAttribute("aria-label", "Chats");
  left.append(newChatButton, newChatMenu, tabs);

  const right = node("div", "shell-right");
  const sessionHeading = node("span", "shell-session-title");
  const pages = node("nav", "shell-pages");
  pages.setAttribute("aria-label", "Pages");
  for (const [id, path] of [["plan", "/plan"]]) {
    const link = node("a", `shell-page ${page === id ? "selected" : ""}`);
    link.dataset.page = id;
    link.innerHTML = '<svg class="shell-page-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M9 4.5A3.5 3.5 0 0 0 5.5 8v.5A3.5 3.5 0 0 0 4 15a3 3 0 0 0 3 3h2m6-13.5A3.5 3.5 0 0 1 18.5 8v.5A3.5 3.5 0 0 1 20 15a3 3 0 0 1-3 3h-2M9 4.5V20m6-15.5V20M9 8H7m8 0h2M9 12H6.5m8.5 0h2.5M9 16H7m8 0h2"/></svg>';
    link.setAttribute("aria-label", "plan");
    link.title = "plan";
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
  settings.addEventListener("click", () => {
    if (page === "chat" && options.switchView) options.switchView("console");
    const closing = settings.getAttribute("aria-expanded") === "true";
    beginNavigation({ kind: "settings", from: closing ? "settings" : page, to: closing ? page : "settings", fullDocument: false, chatID: store.active, mutationToken: store.mutation_token });
  });
  right.append(sessionHeading, pages, settings);
  root.append(left, right);
  document.addEventListener("click", (event) => {
    if (!root.contains(event.target)) for (const menu of root.querySelectorAll(".shell-menu")) menu.hidden = true;
  });

  function report(message) {
    if (options.reportError) options.reportError(message);
    else {
      root.dataset.error = message;
    }
  }

  function sessionsFor(agentID, includeClosed = true) {
    return Object.values(store.sessions)
      .filter((session) => `agent_${session.role === "d" ? "d" : "b"}` === agentID && (includeClosed || !session.closed))
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
	const profile = (store.servers || []).find((item) => item.id === profileID);
    return profile?.label || profileID || agentID;
  }

  function agentState(agentID) {
    const sessions = sessionsFor(agentID, false);
    if (sessions.some((item) => item.model_unreachable)) return "offline";
    if (sessions.some((item) => item.pending_approval || item.pending_repo_policy || item.run?.status === "paused")) return "waiting";
    if (sessions.some((item) => activeRunStates.has(item.run?.status))) return "running";
    return "idle";
  }

  function chatState(session) {
    if (session?.model_unreachable) return "offline";
    if (session?.pending_approval || session?.pending_repo_policy || session?.run?.status === "paused") return "waiting";
    if (activeRunStates.has(session?.run?.status)) return "running";
    return "idle";
  }

  function renderTabs() {
    tabs.replaceChildren();
    const open = Object.values(store.sessions).filter((session) => !session.closed).sort((a, b) => Date.parse(b.created_at || 0) - Date.parse(a.created_at || 0));
    const selectedSession = store.sessions[store.selection.session_id];
    const configured = configuredAgent(selectedSession);
    const hasD = !!String(configured?.d || "").trim();
    newChatButton.title = hasD ? "New chat or plan" : "New chat with agent_b";
    newChatButton.setAttribute("aria-label", newChatButton.title);
    newChatButton.disabled = store.replay || !(store.config.agents || []).length;
    newChatButton.onclick = () => hasD ? showRoleMenu(newChatMenu, newChatButton, configured) : void createChat("agent_b");
    if ((page === "chat" || page === "console") && store.selection.agent_id) rememberAgentSide(store.selection.agent_id, page);
    const rendered = open.length ? open : [null];
    for (const session of rendered) {
      const agentID = `agent_${session?.role === "d" ? "d" : "b"}`;
      const wrap = node("div", "agent-tab-wrap");
      wrap.dataset.agent = agentID;
      if (session) wrap.dataset.session = session.id;
      const selected = !!session && store.selection.session_id === session.id;
      if (selected) wrap.classList.add("selected");
      const chatName = session?.label || agentName(agentID);
      const tab = button("", chatName, `agent-tab ${selected ? "selected" : ""}`);
      const glyphState = session ? chatState(session) : agentState(agentID);
      const side = selected && (page === "chat" || page === "console") ? page : rememberedAgentSide(agentID);
      tab.dataset.agent = agentID;
      if (session) tab.dataset.session = session.id;
      tab.dataset.side = side;
      tab.classList.add(`side-${side}`);
      wrap.classList.add(`side-${side}`);
      tab.setAttribute("aria-label", `${agentID} chat · ${chatName} · ${side}`);
      const robot = agentID.slice(-1);
      tab.innerHTML = `<span class="agent-tab-robot agent-tab-robot-${robot} ${glyphState}" aria-hidden="true"><img src="/static/assets/agent.svg" alt=""><span class="agent-tab-eyes"></span></span><span>${escapeHTML(agentID)}</span>`;
      tab.onclick = () => {
        if (!session) return;
        if (!selected) {
          setSelection(agentID, session.id);
          return;
        }
        const targetSession = session.id;
        const navigation = { kind: "flip", from: page, to: side === "chat" ? "console" : "chat", fullDocument: !options.switchView, chatID: targetSession, mutationToken: store.mutation_token };
        setSelection(agentID, targetSession);
        const next = side === "chat" ? "console" : "chat";
        rememberAgentSide(agentID, next);
        const sessionID = store.selection.session_id;
        const suffix = sessionID ? `?session=${encodeURIComponent(sessionID)}` : "";
        if (options.switchView) options.switchView(next, navigation);
        else requestNavigation(navigation, next === "chat" ? `/chat${suffix}` : `/${suffix}`);
      };
      const menu = node("div", "shell-menu agent-chat-menu");
      menu.hidden = true;
      tab.oncontextmenu = (event) => {
        event.preventDefault();
        for (const other of tabs.querySelectorAll(".shell-menu")) if (other !== menu) other.hidden = true;
        renderAgentMenu(menu, agentID);
        revealMenu(menu, tab);
      };
      wrap.append(tab);
      if (session) {
        const close = button("×", `Close ${chatName}`, "agent-tab-close");
        close.disabled = store.replay || isRunning(session);
        close.onclick = (event) => { event.stopPropagation(); void closeChat(session, menu, agentID); };
        wrap.append(close);
      }
      wrap.append(menu);
      tabs.append(wrap);
    }
  }

  function configuredAgent(session) {
    return (store.config.agents || []).find((agent) => agentKey(agent) === session?.agent_id) || store.config.agents?.[0];
  }

  function showRoleMenu(menu, anchor, configured) {
    menu.replaceChildren();
    const name = configured?.name || "Agent";
    const chat = button(`agent_b · ${name} — chat`, "Open chat", "shell-new-choice");
    chat.onclick = () => { menu.hidden = true; void createChat("agent_b"); };
    const plan = button(`agent_d · ${name} — plan`, "Open plan chat", "shell-new-choice");
    plan.onclick = () => { menu.hidden = true; void createChat("agent_d", configured); };
    menu.append(chat, plan);
    revealMenu(menu, anchor);
  }

  function renderAgentMenu(menu, agentID) {
    const sessions = sessionsFor(agentID, true);
    menu.replaceChildren();
    const openCount = sessions.filter((session) => !session.closed).length;
    const count = node("div", "agent-chat-count");
    count.textContent = `${sessions.length} ${sessions.length === 1 ? "chat" : "chats"} · ${openCount} open · ${sessions.length - openCount} closed`;
    menu.append(count);
    if (!sessions.length) {
      const empty = node("span", "shell-menu-empty");
      empty.textContent = "No chats";
      menu.append(empty);
      return;
    }
    for (const session of sessions) {
      const row = node("div", `agent-chat-row ${session.closed ? "closed" : "open"}`);
      row.dataset.session = session.id;
      const summary = node("span", "agent-chat-summary");
      summary.textContent = `${chatRowText(session)}${session.closed ? " · closed" : ""}`;
      summary.title = firstUserLine(session);
      const open = button("Open", `Open ${firstUserLine(session)}`, "agent-chat-open");
      open.textContent = session.closed ? "Reopen" : "Open";
      open.onclick = async () => {
        if (session.closed) {
          try {
            await api(`/api/sessions/${encodeURIComponent(session.id)}/reopen`, {});
            reduce({ type: "snapshot", data: await api("/api/state", undefined, "GET") });
          } catch (error) { return report(error.message); }
        }
        setSelection(agentID, session.id); menu.hidden = true;
      };
      const rename = button("Rename", `Rename ${firstUserLine(session)}`, "agent-chat-rename");
      rename.onclick = () => showRename(row, session, menu, agentID);
      const close = button("×", `Close ${firstUserLine(session)}`, "agent-chat-close");
      close.disabled = session.closed || store.replay;
      close.onclick = () => void closeChat(session, menu, agentID);
      const remove = button("🗑", `Delete ${firstUserLine(session)} permanently`, "agent-chat-delete");
      remove.disabled = !session.closed || store.replay;
      if (!session.closed) remove.title = "Close this chat before deleting it permanently";
      remove.onclick = () => void armDelete(row, session, menu, agentID, summary, remove);
      row.append(summary, open, rename, close, remove);
      menu.append(row);
    }
  }

  async function armDelete(row, session, menu, agentID, summary, remove) {
    try {
      const preview = await api(`/api/sessions/${encodeURIComponent(session.id)}/delete`, { confirm: false });
      const inventory = preview.inventory || {};
      const writes = inventory.memory_writes || [];
      summary.textContent = `Delete permanently? ${inventory.events || 0} events · ${inventory.jsonl_files || 0} files · ${writes.length} memory kept`;
      const dropLabel = node("label", "agent-chat-drop-memory");
      const dropMemory = document.createElement("input");
      dropMemory.type = "checkbox";
      dropLabel.append(dropMemory, " drop memory");
      remove.textContent = "delete";
      remove.classList.add("armed");
      remove.setAttribute("aria-label", `Confirm permanent delete of ${firstUserLine(session)}`);
      remove.onclick = async () => {
        try {
          await api(`/api/sessions/${encodeURIComponent(session.id)}/delete`, { confirm: true, drop_memory: dropMemory.checked });
          reduce({ type: "snapshot", data: await api("/api/state", undefined, "GET") });
          menu.hidden = true;
        } catch (error) { report(error.message); }
      };
      row.classList.add("delete-confirm");
      row.replaceChildren(summary, ...(writes.length ? [dropLabel] : []), remove);
    } catch (error) { report(error.message); }
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
    try {
      await api(`/api/sessions/${encodeURIComponent(session.id)}`, undefined, "DELETE");
      reduce({ type: "snapshot", data: await api("/api/state", undefined, "GET") });
      renderAgentMenu(menu, agentID);
    } catch (error) { report(error.message); }
  }

  function revealMenu(menu, anchor) {
    menu.hidden = false;
    const anchorRect = anchor.getBoundingClientRect();
    const menuRect = menu.getBoundingClientRect();
    menu.style.left = `${Math.max(8, Math.min(anchorRect.left, innerWidth - menuRect.width - 8))}px`;
    menu.style.right = "auto";
    menu.style.top = `${Math.max(8, Math.min(anchorRect.bottom, innerHeight - menuRect.height - 8))}px`;
  }

  async function createChat(agentID = "agent_b", configured = null) {
    const source = store.sessions[store.selection.session_id] || Object.values(store.sessions).sort((a, b) => Date.parse(b.created_at || 0) - Date.parse(a.created_at || 0))[0];
    try {
      const configuredID = agentKey(configured || configuredAgent(source));
      const body = agentID === "agent_d"
        ? { agent_id: configuredID, role: "d" }
        : source && source.role !== "d" ? { source_session_id: source.id } : { agent_id: configuredID };
      const result = await api("/api/sessions", body);
      reduce({ type: "snapshot", data: await api("/api/state", undefined, "GET") });
      setSelection(agentID, result.session.id);
    } catch (error) { report(error.message); }
  }

  function render() {
    const session = store.sessions[store.selection.session_id];
    document.title = session ? sessionTitle(session) : "Agent_b";
    sessionHeading.hidden = !session;
    sessionHeading.textContent = session ? sessionTitle(session) : "";
    renderTabs();
    const query = new URLSearchParams();
    if (session) query.set("session", session.id);
    const suffix = query.size ? `?${query}` : "";
    for (const link of pages.children) link.href = link.dataset.page === "console" ? `/${suffix}` : `/${link.dataset.page}${suffix}`;
    const configured = configuredAgent(session);
    const planLink = pages.querySelector('[data-page="plan"]');
    if (planLink) planLink.hidden = !session || (session.role !== "d" && !!String(configured?.d || "").trim());
    settings.href = `/${suffix}#settings/servers`;
    options.syncLocation?.(page);
  }

  subscribe((_state, event) => {
    render();
  });
  return {
    render,
    report,
    setPage(next) {
      page = next;
      root.dataset.page = next;
      render();
    },
    newChat() {
      if (!store.replay) void createChat("agent_b");
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
