import { api, reduce, setSelection, store, subscribe } from "./bus.js";
import { chatRowText, closeConfirmText, firstUserLine, isRunning } from "./chat-lifecycle.js";
import { agentTabLayout } from "./agent-tabs.js";
import { installUIErrorRelay } from "./ui-error-relay.js";
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
  const page = options.page || root.dataset.page || "console";
  root.replaceChildren();

  const left = node("div", "shell-left");
  const tabs = node("nav", "agent-tabs");
  tabs.setAttribute("aria-label", "Agents");
  left.append(tabs);

  const right = node("div", "shell-right");
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
    const closing = settings.getAttribute("aria-expanded") === "true";
    beginNavigation({ kind: "settings", from: closing ? "settings" : page, to: closing ? page : "settings", fullDocument: page !== "console", chatID: store.active });
  });
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

  function renderTabs() {
    tabs.replaceChildren();
    const agents = ["agent_b"];
	const selected = store.sessions[store.selection.session_id];
	if ((page === "chat" || page === "console") && store.selection.agent_id) rememberAgentSide(store.selection.agent_id, page);
	const configured = (store.config.agents || []).find((agent) => agentKey(agent) === selected?.agent_id) || store.config.agents?.[0];
	if (configured?.c) agents.push("agent_c");
    if (configured?.d) agents.push("agent_d");
    for (const agentID of agents) {
      const wrap = node("div", "agent-tab-wrap");
      wrap.dataset.agent = agentID;
      const selected = store.selection.agent_id === agentID;
      if (selected) wrap.classList.add("selected");
      const tab = button("", `${agentID} · ${agentName(agentID)}`, `agent-tab ${selected ? "selected" : ""}`);
      const glyphState = agentState(agentID);
      const side = selected && (page === "chat" || page === "console") ? page : rememberedAgentSide(agentID);
      tab.dataset.agent = agentID;
      tab.dataset.side = side;
      tab.removeAttribute("title");
      tab.classList.add(`side-${side}`);
      wrap.classList.add(`side-${side}`);
      tab.setAttribute("aria-label", `${agentID} · ${agentName(agentID)} · ${side}`);
      const robot = agentID.slice(-1);
      tab.innerHTML = `<span class="agent-tab-robot agent-tab-robot-${robot} ${glyphState}" aria-hidden="true"><img src="/static/assets/agent.svg" alt=""><span class="agent-tab-eyes"></span></span><span>${escapeHTML(agentID)}</span>`;
      tab.onclick = () => {
        const current = store.selection.session_id;
        const owned = sessionsFor(agentID, false);
        const targetSession = owned.some((item) => item.id === current) ? current : owned[0]?.id || "";
        beginNavigation({ kind: "flip", from: page, to: side === "chat" ? "console" : "chat", fullDocument: true, chatID: targetSession });
        setSelection(agentID, targetSession);
        const next = side === "chat" ? "console" : "chat";
        rememberAgentSide(agentID, next);
        const sessionID = store.selection.session_id;
        const suffix = sessionID ? `?session=${encodeURIComponent(sessionID)}` : "";
        location.assign(next === "chat" ? `/chat${suffix}` : `/${suffix}`);
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
      if (agentID === "agent_b") {
        const add = button("+", `New chat with ${agentID}`, "agent-tab-new");
        add.disabled = store.replay || !(store.config.agents || []).length;
        add.onclick = (event) => {
          event.stopPropagation();
          for (const other of tabs.querySelectorAll(".shell-menu")) if (other !== menu) other.hidden = true;
          void showNewChatMenu(menu, agentID, add);
        };
        wrap.append(add);
      }
      wrap.append(menu);
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
      if (!overflowMenu.hidden) {
        overflowMenu.hidden = true;
        return;
      }
      renderOverflowMenu(overflowMenu);
      revealMenu(overflowMenu, overflowButton);
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
    const reservedWidth = entries[0]?.getBoundingClientRect().width || 92;
    const layout = agentTabLayout(entries.length, tabs.clientWidth, reservedWidth);
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
      open.disabled = session.closed;
      open.onclick = () => { setSelection(agentID, session.id); menu.hidden = true; };
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
    if (!window.confirm(closeConfirmText(session))) return;
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

  async function showNewChatMenu(menu, agentID = "agent_b", anchor = null) {
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
      if (anchor) revealMenu(menu, anchor);
      else menu.hidden = false;
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
      const tab = tabs.querySelector('.agent-tab-wrap[data-agent="agent_b"] .agent-tab');
      if (menu && tab) void showNewChatMenu(menu, "agent_b", tab);
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
