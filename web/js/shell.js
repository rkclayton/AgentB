import { api, reduce, setSelection, store, subscribe } from "./bus.js";
import { createOperatorStatusController, isOperatorStateEvent } from "./operator-status.js";
import { renderStopState } from "./stop-state.js";
import { chatRowText, closeConfirmText, firstUserLine, isRunning } from "./chat-lifecycle.js";

const activeRunStates = new Set(["running", "queued", "stopping"]);

export function initShell(options = {}) {
  const root = document.getElementById("app-shell");
  if (!root) return null;
  const page = options.page || root.dataset.page || "console";
  root.replaceChildren();

  const left = node("div", "shell-left");
  const tabs = node("nav", "agent-tabs");
  tabs.setAttribute("aria-label", "Agents");
  const addWrap = node("div", "shell-add-wrap");
  const add = button("+", "New chat", "shell-add");
  const addMenu = node("div", "shell-menu shell-new-menu");
  addMenu.hidden = true;
  addWrap.append(add, addMenu);
  left.append(tabs, addWrap);

  const middle = node("div", "shell-middle");
  const selection = node("span", "shell-selection");
  middle.append(selection);

  const right = node("div", "shell-right");
  const stop = button("", "Stop", "stop-sign");
  stop.id = "shell-stop";
  stop.innerHTML = '<span aria-hidden="true"></span>';
  const state = node("span", "shell-state");
  state.id = "shell-state";
  const operator = button("", "Operator mode off", "operator-status");
  operator.id = "shell-operator-status";
  operator.setAttribute("aria-pressed", "false");
  operator.innerHTML = '<img src="/static/assets/operator-off-24.png" srcset="/static/assets/operator-off-48.png 2x" width="24" height="24" alt="">';
  const connection = node("span", "shell-connection");
  connection.id = "connection";
  connection.setAttribute("role", "status");
  const alarm = node("span", "identity-status");
  alarm.id = "shell-identity-alarm";
  alarm.setAttribute("role", "status");
  alarm.hidden = true;
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
  right.append(stop, state, operator, alarm, connection, pages, settings);
  root.append(left, middle, right);

  const operatorControl = createOperatorStatusController(operator, {
    identity: () => store.shell_identity,
    interactive: () => !store.replay,
    setOperatorContext: (enabled) => api("/api/config", { shell: { operator_context: enabled } }),
    reportError: report,
  });

  add.onclick = () => void showNewChatMenu();
  stop.onclick = () => {
    const sessionID = store.selection.session_id;
    if (sessionID && !store.replay) api("/api/stop", { session_id: sessionID }).catch((error) => report(error.message));
  };
  document.addEventListener("click", (event) => {
    if (!addWrap.contains(event.target)) addMenu.hidden = true;
    if (!tabs.contains(event.target)) for (const menu of tabs.querySelectorAll(".shell-menu")) menu.hidden = true;
  });

  function report(message) {
    if (options.reportError) options.reportError(message);
    else {
      connection.textContent = message;
      connection.className = "shell-connection alarm";
    }
  }

  function sessionsFor(agentID, includeClosed = true) {
    if (agentID !== "agent_b") return [];
    return Object.values(store.sessions)
      .filter((session) => includeClosed || !session.closed)
      .sort((a, b) => Date.parse(b.created_at || 0) - Date.parse(a.created_at || 0));
  }

  function agentName(agentID) {
    if (agentID === "agent_b") {
      const selected = store.sessions[store.selection.session_id];
      if (selected?.agent_name) return selected.agent_name;
    }
    const profileID = agentID === "agent_c" ? store.config.roles?.aux : store.config.roles?.main;
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
    if (store.config.roles?.aux) agents.push("agent_c");
    for (const agentID of agents) {
      const wrap = node("div", "agent-tab-wrap");
      const tab = button("", `${agentID} · ${agentName(agentID)}`, `agent-tab ${store.selection.agent_id === agentID ? "selected" : ""}`);
      const glyphState = agentState(agentID);
      tab.dataset.agent = agentID;
      tab.innerHTML = `<span class="agent-state ${glyphState}" aria-hidden="true">${glyphState === "waiting" ? "!" : glyphState === "running" ? "●" : "○"}</span><span>${escapeHTML(agentID)}</span>`;
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
  }

  function renderAgentMenu(menu, agentID) {
    const sessions = sessionsFor(agentID, true);
    menu.replaceChildren();
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

  async function showNewChatMenu() {
    if (store.replay) return;
    try {
      const choices = await api("/api/pick-folder", undefined, "GET");
      addMenu.replaceChildren();
      const addChoice = (label, path) => {
        const choice = button(label, path, "shell-new-choice");
        choice.onclick = () => { addMenu.hidden = true; void createChat(path); };
        addMenu.append(choice);
      };
      addChoice(`Default · ${choices.default}`, choices.default);
      for (const item of (choices.recent || []).filter((item) => item.dir && item.dir.toLowerCase() !== String(choices.default).toLowerCase()).slice(0, 6)) addChoice(item.dir, item.dir);
      const browse = button("Browse…", "Browse for workspace", "shell-new-choice");
      browse.onclick = async () => {
        addMenu.hidden = true;
        try {
          const picked = await api("/api/pick-folder", { default: choices.default });
          await createChat(picked.workspace_dir);
        } catch (error) { if (!String(error.message).includes("canceled")) report(error.message); }
      };
      addMenu.append(browse);
      addMenu.hidden = false;
    } catch (error) { report(error.message); }
  }

  async function createChat(workspace) {
    const source = store.sessions[store.selection.session_id] || Object.values(store.sessions).sort((a, b) => Date.parse(b.created_at || 0) - Date.parse(a.created_at || 0))[0];
    if (!source) return report("No session template is available.");
    try {
      const result = await api("/api/sessions", { source_session_id: source.id, workspace });
      reduce({ type: "snapshot", data: await api("/api/state", undefined, "GET") });
      setSelection("agent_b", result.session.id);
    } catch (error) { report(error.message); }
  }

  function render() {
    const session = store.sessions[store.selection.session_id];
    renderTabs();
    const name = agentName(store.selection.agent_id);
    selection.textContent = session ? `${name} · ${session.label || firstUserLine(session)} · ${session.workspace_dir || session.workspace || "—"}` : name;
    selection.title = selection.textContent;
    const waiting = !!(session?.pending_approval || session?.pending_repo_policy);
    state.textContent = store.replay ? "replay" : waiting ? "waiting for you" : session?.run?.status || "idle";
    state.className = `shell-state ${waiting ? "waiting" : session?.run?.status || "idle"}`;
    renderStopState(stop, session, store.replay);
    operatorControl.render();
    const unavailable = store.shell_identity?.operator_approval_required || store.shell_identity?.fallback;
    alarm.hidden = !unavailable;
    alarm.textContent = unavailable ? `Service identity unavailable · tools require operator approval · ${store.shell_identity.reason}` : "";
    const query = new URLSearchParams();
    if (session) query.set("session", session.id);
    const suffix = query.size ? `?${query}` : "";
    for (const link of pages.children) link.href = link.dataset.page === "console" ? `/${suffix}` : `/${link.dataset.page}${suffix}`;
    settings.href = `/${suffix}#settings/servers`;
    if (page === "chat") history.replaceState(null, "", `/chat${suffix}`);
  }

  subscribe((_state, event) => {
    if (isOperatorStateEvent(event)) operatorControl.render();
    render();
  });
  return { render, report, stop, operator, selection };
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
