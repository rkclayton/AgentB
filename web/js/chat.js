import { api, reduce, setActive, store, subscribe } from "./bus.js";
import { renderMarkdown } from "./markdown.js";
import { operatorLogEntry } from "./operator-log.js";
import { createOperatorStatusController, isOperatorStateEvent } from "./operator-status.js";
import { createThinkingRenderer } from "./reasoning.js";
import { renderBuildHeader } from "./build-header.js";
import { formatDuration } from "./duration.js";
import { renderStopState } from "./stop-state.js";
import { createFileChip, fileURL, filesFromResponse, probeFile } from "./deliverables.js";
import { createApprovalCard } from "./approval.js";
import { callServiceKey, callServiceStatus } from "./call-service-display.js";
import { attachmentChipFile, attachmentMetadata, exchangeFiles, exchangeUpload, uploadAttachment } from "./attachment-upload.js";
import { agentAuthor, chatRowText, closeConfirmText, firstUserLine, isRunning, openSessions, sessionTitle } from "./chat-lifecycle.js";

const binding = document.getElementById("chat-binding");
const closeCurrent = document.getElementById("chat-close");
const newChatButton = document.getElementById("chat-new");
const chatListToggle = document.getElementById("chat-list-toggle");
const chatList = document.getElementById("chat-list");
const status = document.getElementById("chat-status");
const buildID = document.getElementById("chat-build");
const stop = document.getElementById("chat-stop");
const operatorStatus = document.getElementById("chat-operator-status");
const budget = document.getElementById("chat-budget");
const log = document.getElementById("chat-log");
const input = document.getElementById("chat-task");
const send = document.getElementById("chat-send");
const notice = document.getElementById("chat-notice");
const pendingApproval = document.getElementById("chat-pending-approval");
const composer = document.querySelector(".chat-composer");
const attachButton = document.getElementById("chat-attach");
const attachMenu = document.getElementById("chat-attach-menu");
const attachBrowse = document.getElementById("chat-attach-browse");
const attachExchange = document.getElementById("chat-attach-exchange");
const exchangeFileList = document.getElementById("chat-exchange-files");
const filePicker = document.getElementById("chat-file-picker");
const pendingFiles = document.getElementById("chat-attachments");
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
let attachmentsBusy = false;
let chatListExpanded = false;
let dragDepth = 0;
let queuedAttachments = [];
const attachmentQueues = new Map();
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
const fileStates = new Map();
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
    const open = newestOpenSessions();
    if (!store.sessions[bound] || store.sessions[bound].closed) changeBound(requested && store.sessions[requested] && !store.sessions[requested].closed ? requested : open[0]?.id || "");
  }
  if (store.sessions[bound]?.closed) changeBound(newestOpenSessions()[0]?.id || "");
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
  renderChatList();
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
    ? `Service identity unavailable · tools require operator approval · ${store.shell_identity.reason}`
    : "";
}

function renderBinding(session) {
  binding.textContent = sessionTitle(session);
  binding.title = session ? firstUserLine(session) : "";
  closeCurrent.hidden = !session;
  closeCurrent.disabled = !session || !!store.replay;
}

function newestOpenSessions() {
  return openSessions(store.sessions).sort((left, right) => Date.parse(right.created_at || 0) - Date.parse(left.created_at || 0));
}

function renderChatList() {
  const sessions = newestOpenSessions();
  chatListToggle.hidden = sessions.length === 0;
  chatListToggle.textContent = `${chatListExpanded ? "▾" : "▸"} chats (${sessions.length})`;
  chatListToggle.setAttribute("aria-expanded", String(chatListExpanded));
  chatList.hidden = !chatListExpanded;
  if (!chatListExpanded) {
    chatList.replaceChildren();
    return;
  }
  chatList.replaceChildren(...sessions.map((session) => {
    const row = document.createElement("div");
    row.className = `chat-list-row ${session.id === bound ? "current" : ""}`;
    const select = document.createElement("button");
    select.type = "button";
    select.className = "chat-list-select";
    const text = chatRowText(session);
    select.append(document.createTextNode(text.slice(0, -1)));
    const glyph = document.createElement("span");
    glyph.className = `chat-state-glyph ${session.run?.status || "idle"}`;
    glyph.textContent = text.slice(-1);
    select.append(glyph);
    select.title = `${firstUserLine(session)} · ${session.run?.status || "idle"}`;
    select.onclick = () => {
      chatListExpanded = false;
      changeBound(session.id);
      follow = true;
      page = 0;
      render();
    };
    const close = document.createElement("button");
    close.type = "button";
    close.className = "chat-list-close";
    close.textContent = "×";
    close.title = "Close chat";
    close.setAttribute("aria-label", `Close ${firstUserLine(session)}`);
    close.disabled = !!store.replay;
    close.onclick = () => void closeChat(session);
    row.append(select, close);
    return row;
  }));
}

function renderHeader(session) {
	status.textContent = store.replay ? "replay" : session?.pending_approval ? "waiting for you" : session?.run?.status || "idle";
	status.classList.toggle("waiting", !store.replay && !!session?.pending_approval);
  renderBuildHeader(buildID, document.getElementById("chat-signature-state"), store.build, store.signature);
  renderStopState(stop, session, store.replay);
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
    empty.textContent = "No open chats.";
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
  return groupResponses(session.chat || []);
}

function changeBound(value) {
  if (bound) attachmentQueues.set(bound, queuedAttachments);
  bound = value;
  if (bound) setActive(bound);
  queuedAttachments = attachmentQueues.get(bound) || [];
  const query = new URLSearchParams(location.search);
  if (bound) query.set("session", bound); else query.delete("session");
  history.replaceState(null, "", `/chat${query.size ? `?${query}` : ""}`);
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

function renderEntry(session, entry) {
  if (entry.type === "notice") return renderNotice(session, entry);
  if (entry.type === "response") return renderResponse(session, entry);
  let view = entryViews.get(entry.key);
  if (!view) {
    const row = document.createElement("section");
    row.tabIndex = 0;
    const content = document.createElement("div");
    content.className = "chat-content";
    const author = speaker(entry.type === "user" ? "you" : agentAuthor(session, entry.agentRole));
    row.append(author, content);
    view = { row, author, content, text: "" };
    entryViews.set(entry.key, view);
  }
  usedEntryViews.add(entry.key);
  view.author.lastElementChild.textContent = entry.type === "user" ? "you" : agentAuthor(session, entry.agentRole);
  view.row.className = `chat-entry ${entry.type === "user" ? "chat-user" : entry.type === "tool" ? "tool-entry" : "chat-agent"}`;
  const content = view.content;
  if (entry.type === "user") {
    const nodes = [];
    if (entry.text) {
      if (!view.userText) view.userText = document.createElement("div");
      if (view.text !== entry.text) view.userText.textContent = entry.text;
      nodes.push(view.userText);
    }
    for (const attachment of entry.attachments || []) {
      nodes.push(renderFileChip(session, attachmentChipFile(attachment)));
    }
    reconcileChildren(content, nodes);
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
    const author = speaker(agentAuthor(session));
    row.append(author, content);
    view = { row, author, content, items: new Map() };
    entryViews.set(entry.key, view);
  }
  usedEntryViews.add(entry.key);
  view.author.lastElementChild.textContent = agentAuthor(session);
  const nodes = [];
  const usedItems = new Set();
  for (const item of entry.items) {
    usedItems.add(item.key);
    if (item.type === "notice") {
		const notice = noticeContent(session, item, false);
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
  const files = filesFromResponse(entry.items);
  if (files.length) {
    let chips = view.chips;
    if (!chips) {
      chips = document.createElement("div");
      chips.className = "file-chips";
      view.chips = chips;
    }
    chips.replaceChildren(...files.map((file) => renderFileChip(session, file)));
    nodes.push(chips);
  }
  reconcileChildren(view.content, nodes);
  for (const key of view.items.keys()) if (!usedItems.has(key)) view.items.delete(key);
  return view.row;
}

function renderFileChip(session, file) {
  const key = `${session.id}:${file.path.toLowerCase()}:${file.callID}`;
  let state = fileStates.get(key);
  if (!state) {
    state = { state: "checking", bytes: file.bytes };
    fileStates.set(key, state);
    probeFile(fileURL(session.id, file.path)).then((next) => {
      fileStates.set(key, next);
      schedule();
    });
  }
  return createFileChip(document, file, state, {
    downloadURL: fileURL(session.id, file.path),
    openFolder: async () => {
      try {
        await api("/api/open-folder", { session_id: session.id, path: file.openPath, scope: file.openScope });
      } catch (error) {
        localNotice = error.message || String(error);
        localAlarm = true;
        renderComposer(session);
      }
    },
  });
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
  button.children[2].textContent = callServiceStatus(entry.name, entry.result) || state;
  button.children[2].className = `tool-state ${state === "error" ? "error" : ""}`;
  button.children[3].textContent = formatDuration(entry.result?.ms);
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
	const content = noticeContent(session, entry, false);
  if (content.classList.contains("alarm")) row.classList.add("alarm");
  row.append(content);
  return row;
}

function noticeContent(session, entry, actionable) {
  const event = entry.event;
  const data = event.data || {};
  const content = document.createElement("div");
  content.className = "chat-content";
  if (event.type === "run.stopped") {
    const reason = (data.reason || "").replaceAll("_", " ");
    content.textContent = `stopped: ${reason}${data.reason === "turn_ceiling" ? ` (${data.turns || session.run.max_turns})` : data.detail ? `, ${data.detail}` : ""}`;
    if (data.reason !== "done") content.classList.add("alarm");
  } else if (event.type === "files.delivered") {
    const items = data.items || [];
    if (!items.length) content.hidden = true;
    else {
      const copied = items.filter((item) => item.status === "copied").length;
      const identical = items.filter((item) => item.status === "identical").length;
      const failed = items.filter((item) => item.status === "failed").length;
      const parts = [];
      if (copied) parts.push(`${copied} copied to ${data.exchange_folder}`);
      if (identical) parts.push(`${identical} identical skipped`);
      if (failed) parts.push(`${failed} failed`);
      content.textContent = `delivery: ${parts.join(" · ")}`;
      if (failed) content.classList.add("alarm");
    }
  } else if (event.type === "run.queued") content.textContent = `waiting for a slot (position ${data.position})`;
  else if (event.type === "message.queued") content.textContent = `queued (${data.position})`;
  else if (event.type === "compaction") content.textContent = `compacted ${signed((data.after || 0) - (data.before || 0))} tokens${data.profile_id ? ` via ${data.profile_id}` : ""}`;
  else if (event.type === "workspace.conflict") {
    content.textContent = `conflict: ${data.path} written by ${data.other_label} ${data.age_s} s ago`;
    content.classList.add("alarm");
  } else if (event.type === "memory.noted") content.textContent = "noted for next session";
  else if (event.type === "operator.context") {
    const entry = operatorLogEntry(data);
    content.textContent = entry.text;
    if (entry.alarm) content.classList.add("operator-mode-enabled");
  }
	else if (event.type === "shell.grant") {
		const executable = data.executable ? ` · ${data.executable}` : "";
		content.textContent = `shell grant: ${String(data.rule || "shell").replaceAll("_", " ")} · ${data.identity || "service"} · for this ${data.scope === "session" ? "chat" : "run"}${executable}`;
	}
  else if (event.type === "shell.grant_lapsed") {
    const executable = data.executable ? ` · ${data.executable}` : "";
		content.textContent = `shell grant lapsed: ${String(data.rule || "shell").replaceAll("_", " ")}${executable}`;
	}
	else if (event.type === "file.grant") content.textContent = `file-tool grant: operator · for this ${data.scope === "session" ? "chat" : "run"}`;
	else if (event.type === "file.grant_lapsed") content.textContent = `file-tool grant lapsed: ${data.scope === "session" ? "chat closed" : "run ended"}`;
	else if (event.type === "approval.required") {
		return createApprovalCard(document, entry, {
			replay: store.replay,
			decide: actionable ? (callID, decision) => api("/api/approve", { session_id: session.id, call_id: callID, decision }) : null,
		});
  }
  if (entry.agentRole === "aux") {
    const label = document.createElement("span");
    label.className = "chat-notice-author";
    label.textContent = `${agentAuthor(session, "aux")} · `;
    content.prepend(label);
  }
  return content;
}

function renderComposer(session) {
	document.body.classList.toggle("no-open-chats", !session);
	send.textContent = "Send";
  send.disabled = !session || !!store.replay;
  input.disabled = !session || !!store.replay;
  attachButton.disabled = !session || !!store.replay || attachmentsBusy;
  if (store.replay) input.placeholder = "Replay";
  else if (session?.run.status === "paused") input.placeholder = "paused — waiting for approval";
  else input.placeholder = "Send a task · Enter sends · Shift+Enter newline";
  const queued = session?.queued_messages || 0;
	const message = localNotice || (session && !session.runnable ? session.not_runnable_reason : queued ? `queued (${queued})` : session?.pending_approval ? "waiting for you" : "");
  notice.textContent = message;
  notice.className = `chat-notice ${localAlarm || (session && !session.runnable) ? "alarm" : ""}`;
	pendingFiles.replaceChildren(...queuedAttachments.map((file) => {
    const row = document.createElement("span");
    row.className = "chat-pending-file";
    row.textContent = `${file.path.split("/").pop()} · ${format(file.bytes)} B${file.reused ? " · reused" : ""}`;
    return row;
	}));
	pendingApproval.hidden = !session?.pending_approval;
	pendingApproval.replaceChildren(...(session?.pending_approval ? [createApprovalCard(document, session.pending_approval, {
		replay: store.replay,
		decide: (callID, decision) => api("/api/approve", { session_id: session.id, call_id: callID, decision }),
	})] : []));
}

async function newChat() {
  if (store.replay) return;
  const source = store.sessions[bound] || Object.values(store.sessions).sort((left, right) => Date.parse(right.created_at || 0) - Date.parse(left.created_at || 0))[0];
  if (!source) {
    localNotice = "No session template is available.";
    localAlarm = true;
    return renderComposer(undefined);
  }
  try {
    const result = await api("/api/sessions", { source_session_id: source.id });
    reduce({ type: "snapshot", data: await api("/api/state", undefined, "GET") });
    changeBound(result.session.id);
    chatListExpanded = false;
    localNotice = "";
    localAlarm = false;
    follow = true;
    page = 0;
    render();
    input.focus();
  } catch (error) {
    localNotice = error.message || String(error);
    localAlarm = true;
    renderComposer(store.sessions[bound]);
  }
}

async function closeChat(session) {
  if (!session || store.replay) return;
  if (isRunning(session)) {
    localNotice = "This chat has a running run. Stop it before closing the chat.";
    localAlarm = true;
    return renderComposer(store.sessions[bound]);
  }
  if (!window.confirm(closeConfirmText(session))) return;
  try {
    await api(`/api/sessions/${encodeURIComponent(session.id)}`, undefined, "DELETE");
    reduce({ type: "snapshot", data: await api("/api/state", undefined, "GET") });
    if (bound === session.id) changeBound(newestOpenSessions()[0]?.id || "");
    chatListExpanded = false;
    localNotice = "";
    localAlarm = false;
    render();
  } catch (error) {
    localNotice = error.message || String(error);
    localAlarm = true;
    renderComposer(store.sessions[bound]);
  }
}

async function submit() {
  const session = store.sessions[bound];
  if (!session || store.replay) return;
  const text = input.value.trim();
  if (!text && !queuedAttachments.length) return;
  try {
		await api("/api/message", { session_id: session.id, text, attachments: queuedAttachments.map(attachmentMetadata) });
    input.value = "";
    queuedAttachments = [];
    attachmentQueues.set(bound, queuedAttachments);
    resize();
		localNotice = "";
    localAlarm = false;
  } catch (error) {
    localNotice = error.message;
    localAlarm = true;
  }
  renderComposer(session);
}

async function queueFiles(files) {
  const session = store.sessions[bound];
  if (!session || store.replay || !files.length) return;
  attachmentsBusy = true;
  localNotice = "Uploading attachment…";
  localAlarm = false;
  renderComposer(session);
  try {
    for (const file of files) {
      const uploaded = await uploadAttachment(file, session.id, { token: store.mutation_token });
      if (!queuedAttachments.some((item) => item.path.toLowerCase() === uploaded.path.toLowerCase())) queuedAttachments.push(uploaded);
      localNotice = uploaded.note || "Attachment ready";
    }
  } catch (error) {
    localNotice = error.message || String(error);
    localAlarm = true;
  } finally {
    attachmentsBusy = false;
    renderComposer(session);
  }
}

async function refreshExchangeFiles() {
  if (store.replay || !store.sessions[bound]) return;
  try {
    const files = await exchangeFiles();
    exchangeFileList.replaceChildren(...files.map((file) => {
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = `${file.path} · ${format(file.bytes)} B`;
      button.onclick = () => void queueExchangeFile(file);
      return button;
    }));
    if (!files.length) {
      const empty = document.createElement("span");
      empty.textContent = "Exchange folder is empty";
      exchangeFileList.append(empty);
    }
  } catch (error) {
    localNotice = error.message || String(error);
    localAlarm = true;
    renderComposer(store.sessions[bound]);
  }
}

async function queueExchangeFile(item) {
  const session = store.sessions[bound];
  if (!session || !item) return;
  attachmentsBusy = true;
  localNotice = "Copying from exchange folder…";
  localAlarm = false;
  renderComposer(session);
  try {
    const uploaded = await exchangeUpload(item, session.id, { token: store.mutation_token, maxBytes: store.config.tools?.attachments?.max_bytes });
    if (!queuedAttachments.some((value) => value.path.toLowerCase() === uploaded.path.toLowerCase())) queuedAttachments.push(uploaded);
    localNotice = uploaded.note || "Attachment ready";
    attachMenu.hidden = true;
  } catch (error) {
    localNotice = error.message || String(error);
    localAlarm = true;
  } finally {
    attachmentsBusy = false;
    renderComposer(session);
  }
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

send.onclick = submit;
stop.onclick = stopRun;
newChatButton.onclick = () => void newChat();
closeCurrent.onclick = () => void closeChat(store.sessions[bound]);
chatListToggle.onclick = () => {
  chatListExpanded = !chatListExpanded;
  renderChatList();
};
attachButton.onclick = () => { attachMenu.hidden = !attachMenu.hidden; };
attachBrowse.onclick = () => { attachMenu.hidden = true; filePicker.click(); };
attachExchange.onclick = () => void refreshExchangeFiles();
filePicker.addEventListener("change", () => {
  void queueFiles([...filePicker.files]);
  filePicker.value = "";
});
document.body.addEventListener("dragenter", (event) => {
  if (!store.replay && event.dataTransfer?.types?.includes("Files")) {
    event.preventDefault();
    dragDepth++;
    document.body.classList.add("drop-target");
  }
});
document.body.addEventListener("dragover", (event) => {
  if (!store.replay && event.dataTransfer?.types?.includes("Files")) event.preventDefault();
});
document.body.addEventListener("dragleave", () => {
  dragDepth = Math.max(0, dragDepth - 1);
  if (!dragDepth) document.body.classList.remove("drop-target");
});
document.body.addEventListener("drop", (event) => {
  dragDepth = 0;
  document.body.classList.remove("drop-target");
  if (!store.replay && event.dataTransfer?.files?.length) {
    event.preventDefault();
    void queueFiles([...event.dataTransfer.files]);
  }
});
input.addEventListener("input", resize);
input.addEventListener("paste", (event) => {
  const files = [...(event.clipboardData?.files || [])];
  if (files.length) {
    event.preventDefault();
    void queueFiles(files);
  }
});
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
  const service = callServiceKey(args);
  if (service) return service;
  for (const key of ["path", "command", "pattern", "note"]) if (args[key] !== undefined) return String(args[key]);
  const first = Object.values(args)[0];
  return first === undefined ? "" : typeof first === "string" ? first : JSON.stringify(first);
}
function capResult(value) {
  const lines = String(value || "").split("\n");
  return lines.length <= 200 ? lines.join("\n") : [...lines.slice(0, 199), "[… open in timeline for the rest]"].join("\n");
}
function signed(value) {
  return `${value < 0 ? "−" : value > 0 ? "+" : "±"}${format(Math.abs(value))}`;
}
function format(value) {
  return Number(value || 0).toLocaleString("en-US");
}
