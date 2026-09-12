import { api, reduce, setSelection, store, subscribe } from "./bus.js";
import { initShell } from "./shell.js";
import { renderMarkdown } from "./markdown.js";
import { operatorLogEntry } from "./operator-log.js";
import { createThinkingRenderer } from "./reasoning.js";
import { formatDuration } from "./duration.js";
import { createFileChip, fileURL, filesFromResponse, probeFile } from "./deliverables.js";
import { createApprovalCard } from "./approval.js";
import { callServiceKey, callServiceStatus } from "./call-service-display.js";
import { attachmentChipFile, attachmentMetadata, exchangeFiles, exchangeUpload, uploadAttachment } from "./attachment-upload.js";
import { agentAuthor, isRunning, openSessions } from "./chat-lifecycle.js";
import { renderStopState } from "./stop-state.js";
import { groupResponseRows, hasVisibleChatContent, itemFailed, responseBlocks, responseSummary } from "./chat-response-groups.js";
import { navigationSurfaceReady } from "./navigation-telemetry.js";

const budget = document.getElementById("chat-budget");
const log = document.getElementById("chat-log");
const input = document.getElementById("chat-task");
const expandComposer = document.getElementById("chat-expand");
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
const stop = document.getElementById("chat-stop");
const retryModel = document.getElementById("chat-retry-model");
let requested = new URLSearchParams(location.search).get("session");
const selectedID = () => store.selection.session_id;
const expanded = new Set();
let follow = true;
let page = 0;
let localNotice = "";
let localAlarm = false;
let frame = 0;
let renderTimer = 0;
let attachmentsBusy = false;
let dragDepth = 0;
let queuedAttachments = [];
let composerExpanded = false;
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
const fileViews = new Map();
const toolViews = new Map();
let usedEntryViews = new Set();
let usedFileViews = new Set();
let usedToolViews = new Set();
const earlierButton = document.createElement("button");
earlierButton.type = "button";
earlierButton.className = "chat-earlier";
earlierButton.onclick = () => {
  page++;
  follow = false;
  renderLog(store.sessions[selectedID()]);
};
const jumpButton = document.createElement("button");
jumpButton.type = "button";
jumpButton.className = "chat-jump";
jumpButton.textContent = "Jump to latest";
jumpButton.onclick = () => {
  follow = true;
  page = 0;
  renderLog(store.sessions[selectedID()]);
};
const shell = initShell({ page: "chat", reportError: (message) => {
  localNotice = message;
  localAlarm = true;
  renderComposer(store.sessions[selectedID()]);
} });

subscribe((_state, event) => {
  if (event.type === "snapshot") {
    const open = newestOpenSessions();
    if (requested && store.sessions[requested] && !store.sessions[requested].closed) { changeBound(requested); requested = ""; }
    else if (store.selection.agent_id === "agent_b" && (!store.sessions[selectedID()] || store.sessions[selectedID()].closed)) changeBound(open[0]?.id || "");
  }
  if (store.selection.agent_id === "agent_b" && store.sessions[selectedID()]?.closed) changeBound(newestOpenSessions()[0]?.id || "");
  if (event.session_id && selectedID() && event.session_id !== selectedID()) return;
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
  const session = store.sessions[selectedID()];
  renderBudget(session);
  renderLog(session);
  renderComposer(session);
  navigationSurfaceReady("chat", store);
}

function newestOpenSessions() {
  return openSessions(store.sessions).sort((left, right) => Date.parse(right.created_at || 0) - Date.parse(left.created_at || 0));
}

function renderBudget(session) {
  const value = session?.budget || {};
  const used = value.used_measured || value.used_est || 0;
  const ceiling = value.ceiling || 0;
  const ratio = ceiling ? used / ceiling : 0;
  budget.className = `chat-budget ${ratio > 1 ? "over" : ratio > 0.85 ? "warn" : ""}`;
  budget.querySelector(".chat-budget-fill").style.width = `${Math.min(100, ratio * 100)}%`;
  budget.querySelector(".chat-budget-tip").textContent = `${value.estimated ? "estimated · " : ""}${format(used)} / ${format(ceiling)}`;
}

function renderLog(session) {
  const wasBottom = follow;
  thinkingRenderer.begin();
  usedEntryViews = new Set();
  usedFileViews = new Set();
  usedToolViews = new Set();
  if (!session) {
    const empty = document.createElement("div");
    empty.className = "chat-empty";
    const noAgent = !(store.config.agents || []).length;
    const face = document.createElement("img");
    face.src = "/static/assets/operator-off-48.png";
    face.width = 48; face.height = 48; face.alt = "";
    const line = document.createElement("span");
    if (noAgent) {
      line.append("No agent connected — add one in ");
      const settings = document.createElement("a"); settings.textContent = "Settings"; settings.href = "/#settings/servers";
      line.append(settings);
    } else {
      const launch = document.createElement("button"); launch.type = "button"; launch.textContent = "New chat"; launch.onclick = () => shell?.newChat();
      line.append(launch);
    }
    empty.append(face, line);
    finishLogRender([empty]);
    return;
  }
  let entries;
  try {
    entries = buildEntries(session);
  } catch (error) {
    finishLogRender([renderHistoryFailure(error)]);
    return;
  }
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
  for (const [index, entry] of entries.slice(start, end).entries()) nodes.push(renderEntrySafely(session, entry, start + index));
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
  for (const key of fileViews.keys()) if (!usedFileViews.has(key)) fileViews.delete(key);
  for (const key of toolViews.keys()) if (!usedToolViews.has(key)) toolViews.delete(key);
}

function reconcileChildren(parent, nodes) {
  for (let index = 0; index < nodes.length; index++) {
    if (parent.children[index] !== nodes[index])
      parent.insertBefore(nodes[index], parent.children[index] || null);
  }
  while (parent.children.length > nodes.length) parent.lastElementChild.remove();
}

function buildEntries(session) {
  const chat = Array.isArray(session?.chat) ? session.chat : [];
  return groupResponses(chat.filter((entry) => !["operator.context", "message.queued", "run.queued"].includes(entry?.event?.type)).filter(hasVisibleChatContent));
}

function changeBound(value) {
  const previous = selectedID();
  if (previous) attachmentQueues.set(previous, queuedAttachments);
  setSelection("agent_b", value);
  queuedAttachments = attachmentQueues.get(value) || [];
}

function groupResponses(entries) {
  const grouped = [];
  let response = null;
  let boundary = "orphan";
  for (const entry of entries) {
    if (entry?.type === "user" || entry?.type === "summary") {
      grouped.push(entry);
      boundary = entry.key || `${entry.type}:${grouped.length}`;
      response = null;
      continue;
    }
    if (!entry || typeof entry !== "object") {
      grouped.push(entry);
      response = null;
      continue;
    }
    if (entry.type === "notice" && entry.event?.type === "run.stopped" && entry.event?.data?.reason === "model_unreachable") {
      grouped.push(entry);
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

function renderEntrySafely(session, entry, index) {
  try {
    return renderEntry(session, entry);
  } catch (error) {
    return renderFailure(entry, error, index, false);
  }
}

function renderEntry(session, entry) {
  if (!entry || typeof entry !== "object") throw new Error("entry is missing or is not an object");
  if (!entry.key) throw new Error("entry key is missing");
  if (entry.type === "notice") return renderNotice(session, entry);
  if (entry.type === "response") return renderResponse(session, entry);
  let view = entryViews.get(entry.key);
  if (!view) {
    const row = document.createElement("section");
    row.tabIndex = 0;
    const content = document.createElement("div");
    content.className = "chat-content";
    const author = speaker(entry.type === "user" ? "you" : entry.type === "summary" ? "summary" : agentAuthor(session, entry.agentRole), entry.type !== "user" && entry.type !== "summary");
    row.append(author, content);
    view = { row, author, content, text: "" };
    entryViews.set(entry.key, view);
  }
  usedEntryViews.add(entry.key);
  view.row.dataset.entryKey = entry.key;
  view.author.lastElementChild.textContent = entry.type === "user" ? "you" : entry.type === "summary" ? "summary" : agentAuthor(session, entry.agentRole);
  view.row.className = `chat-entry ${entry.type === "user" ? "chat-user" : entry.type === "summary" ? "chat-summary" : entry.type === "tool" ? "tool-entry" : "chat-agent"}`;
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
  else if (entry.type === "summary") {
    if (view.text !== entry.text) content.textContent = entry.text || "";
    view.text = entry.text || "";
  }
  else throw new Error(`unsupported top-level entry type ${String(entry.type || "(missing)")}`);
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
    const summary = document.createElement("button");
    summary.type = "button";
    summary.className = "chat-response-summary";
    summary.onclick = () => {
      const allOpen = view.stepKeys.length > 0 && view.stepKeys.every((key) => expanded.has(key));
      for (const key of view.stepKeys) allOpen ? expanded.delete(key) : expanded.add(key);
      render();
    };
    const rows = document.createElement("div");
    rows.className = "chat-response-rows";
    content.append(summary, rows);
    const author = speaker(agentAuthor(session), true);
    row.append(author, content);
    view = { row, author, content, summary, rows, blocks: new Map(), stepKeys: [] };
    entryViews.set(entry.key, view);
  }
  usedEntryViews.add(entry.key);
  view.author.lastElementChild.textContent = agentAuthor(session);
  const totals = responseSummary(entry.items);
  const active = isRunning(session) && entry.items.some((item) => item?.run_id && item.run_id === session.run?.run_id);
  const blocks = responseBlocks(entry.items);
  view.stepKeys = blocks.filter((block) => block.steps.length).map((block) => block.key);
  const open = active || (view.stepKeys.length > 0 && view.stepKeys.every((key) => expanded.has(key)));
  view.summary.setAttribute("aria-expanded", String(open));
  view.summary.textContent = `${open ? "▾" : "▸"} ${responseSummaryText(totals, entry.items.length)}`;
  view.row.classList.toggle("alarm", totals.failed > 0);
  const usedBlocks = new Set(blocks.map((block) => block.key));
  reconcileChildren(view.rows, blocks.map((block) => renderResponseBlock(session, view, block, active)));
  for (const key of view.blocks.keys()) if (!usedBlocks.has(key)) view.blocks.delete(key);
  return view.row;
}

function renderResponseBlock(session, view, block, active) {
  let blockView = view.blocks.get(block.key);
  if (!blockView) {
    const root = document.createElement("div");
    root.className = "chat-response-block";
    blockView = { root, items: new Map(), prose: null, proseText: "", proseCaret: null, fold: null, head: null, rows: null };
    view.blocks.set(block.key, blockView);
  }
  const nodes = [];
  if (block.prose) nodes.push(renderResponseProse(blockView, block.prose));
  if (block.steps.length) nodes.push(renderResponseStepFold(session, blockView, block, active));
  reconcileChildren(blockView.root, nodes);
  return blockView.root;
}

function renderResponseProse(view, item) {
  if (!view.prose) {
    view.prose = document.createElement("div");
    view.prose.className = "chat-response-prose";
    view.answer = document.createElement("div");
    view.answer.className = "chat-response-answer";
  }
  view.prose.dataset.entryKey = item.key;
  if (view.proseText !== item.text) renderMarkdown(view.answer, item.text);
  view.proseText = item.text;
  const nodes = [view.answer];
  if (!item.done) {
    if (!view.proseCaret) {
      view.proseCaret = document.createElement("span");
      view.proseCaret.className = "stream-caret";
    }
    nodes.push(view.proseCaret);
  }
  reconcileChildren(view.prose, nodes);
  return view.prose;
}

function renderResponseStepFold(session, view, block, active) {
  if (!view.fold) {
    view.fold = document.createElement("div");
    view.fold.className = "chat-step-fold";
    view.head = document.createElement("button");
    view.head.type = "button";
    view.head.className = "chat-step-summary";
    view.head.onclick = () => {
      expanded.has(block.key) ? expanded.delete(block.key) : expanded.add(block.key);
      render();
    };
    view.rows = document.createElement("div");
    view.rows.className = "chat-step-rows";
    view.fold.append(view.head, view.rows);
  }
  const totals = responseSummary(block.steps);
  const open = active || expanded.has(block.key);
  view.fold.classList.toggle("alarm", totals.failed > 0);
  view.head.setAttribute("aria-expanded", String(open));
  view.head.textContent = `${open ? "▾" : "▸"} ${responseSummaryText(totals, block.steps.length)}`;
  const usedItems = new Set(block.steps.map((item, index) => item?.key || `invalid:${index}`));
  const nodes = [];
  if (open) {
    const rows = active ? block.steps : groupResponseRows(block.steps);
    for (const [index, item] of rows.entries()) {
      try {
        if (item?.kind === "tool-group") {
          usedItems.add(item.key);
          nodes.push(renderResponseToolGroup(session, view, item));
        } else {
          const key = item?.key || `invalid:${index}`;
          nodes.push(renderResponseItem(session, view, item, key));
        }
      } catch (error) {
        nodes.push(renderFailure(item, error, index, true));
      }
    }
    const files = filesFromResponse(block.steps.filter((item) => item && typeof item === "object"));
    if (files.length) {
      if (!view.chips) {
        view.chips = document.createElement("div");
        view.chips.className = "file-chips";
      }
      reconcileChildren(view.chips, files.map((file) => renderFileChip(session, file)));
      nodes.push(view.chips);
    }
  }
  reconcileChildren(view.rows, nodes);
  for (const key of view.items.keys()) if (!usedItems.has(key)) view.items.delete(key);
  return view.fold;
}

function responseSummaryText(summary, rowCount) {
  const parts = [];
  if (summary.tools) parts.push(`${summary.tools} tool ${summary.tools === 1 ? "call" : "calls"}`);
  if (summary.failed) parts.push(`${summary.failed} failed`);
  if (summary.thoughts) parts.push(`${summary.thoughts} ${summary.thoughts === 1 ? "thought" : "thoughts"}`);
  if (!summary.tools && !summary.thoughts && summary.answers) parts.push(`${summary.answers} ${summary.answers === 1 ? "answer" : "answers"}`);
  if (!parts.length) parts.push(`${rowCount} ${rowCount === 1 ? "row" : "rows"}`);
  if (summary.duration) parts.push(formatDuration(summary.duration));
  return parts.join(" · ");
}

function renderResponseToolGroup(session, view, group) {
  let groupView = view.items.get(group.key);
  if (!groupView) {
    const root = document.createElement("div");
    root.className = "chat-tool-group";
    const head = document.createElement("button");
    head.type = "button";
    head.className = "chat-tool-group-head";
    head.onclick = () => {
      expanded.has(group.key) ? expanded.delete(group.key) : expanded.add(group.key);
      render();
    };
    const children = document.createElement("div");
    children.className = "chat-tool-group-calls";
    root.append(head, children);
    groupView = { root, head, children, group: true };
    view.items.set(group.key, groupView);
  }
  const open = expanded.has(group.key);
  const parts = [`${group.tool} ×${group.calls}`];
  if (group.thoughts) parts.push(`+${group.thoughts} ${group.thoughts === 1 ? "thought" : "thoughts"}`);
  if (group.failed) parts.push(`${group.failed} failed`);
  if (group.duration) parts.push(formatDuration(group.duration));
  groupView.root.classList.toggle("alarm", group.failed > 0);
  groupView.head.setAttribute("aria-expanded", String(open));
  groupView.head.textContent = `${open ? "▾" : "▸"} ${parts.join(" · ")}`;
  const children = open ? group.items.map((item, index) => {
    try {
      if (item?.type === "agent" && item.reasoning) expanded.add(item.key);
      return renderResponseItem(session, view, item, item?.key || `invalid:group:${index}`, item?.type === "tool");
    }
    catch (error) {
      return renderFailure(item, error, index, true);
    }
  }) : [];
  reconcileChildren(groupView.children, children);
  return groupView.root;
}

function renderResponseItem(session, view, item, key, forceToolOpen = false) {
  if (!item || typeof item !== "object") throw new Error("entry is missing or is not an object");
  if (!item.key) throw new Error("entry key is missing");
  if (item.type === "notice") {
    const notice = noticeContent(session, item, false);
    notice.classList.add("chat-response-notice");
    notice.dataset.entryKey = item.key;
    return notice;
  }
  let itemView = view.items.get(key);
    if (!itemView) {
      const step = document.createElement("div");
      step.className = `chat-response-step ${item.type === "tool" ? "chat-response-tool" : ""}`;
      itemView = { step, answer: null, caret: null, answerText: "" };
      view.items.set(key, itemView);
    }
    itemView.step.dataset.entryKey = item.key;
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
      stepNodes.push(toolTick(item, forceToolOpen));
    } else {
      throw new Error(`unsupported entry type ${String(item.type || "(missing)")}`);
    }
    reconcileChildren(itemView.step, stepNodes);
    return itemView.step;
}

function renderFailure(entry, error, index, nested) {
  const row = document.createElement(nested ? "div" : "section");
  row.className = nested ? "chat-response-step chat-response-notice chat-render-failure" : "chat-entry chat-notice-row chat-render-failure";
  const key = safeEntryValue(entry, "key");
  if (key) row.dataset.entryKey = key;
  const content = document.createElement("div");
  content.className = "chat-content";
  const label = entryLabel(entry, index);
  const reason = errorReason(error);
  content.textContent = `${label} could not render · ${reason}`;
  row.append(content);
  console.error("chat render failure", label, reason);
  return row;
}

function renderHistoryFailure(error) {
  const row = document.createElement("div");
  row.className = "chat-empty chat-render-failure";
  const reason = errorReason(error);
  row.textContent = `Chat history could not render · ${reason}`;
  console.error("chat render failure", "history", reason);
  return row;
}

function entryLabel(entry, index) {
  if (!entry || typeof entry !== "object") return `entry ${index + 1}`;
  const type = safeEntryValue(entry, "type");
  const key = safeEntryValue(entry, "key");
  if (type === "tool") return `tool ${safeEntryValue(entry, "name") || safeEntryValue(entry, "callID") || key || index + 1}`;
  if (type === "notice") return `notice ${safeEntryValue(safeEntryValue(entry, "event"), "type") || key || index + 1}`;
  return `${type || "entry"} ${key || index + 1}`;
}

function errorReason(error) {
  let value = error;
  try { value = error?.message || error; } catch {}
  try { return String(value || "unknown render failure").replace(/\s+/g, " ").slice(0, 240); }
  catch { return "unknown render failure"; }
}

function safeEntryValue(entry, key) {
  try { return entry && typeof entry === "object" ? entry[key] : undefined; }
  catch { return undefined; }
}

function renderFileChip(session, file) {
  const key = `${session.id}:${file.path.toLowerCase()}:${file.callID}`;
  usedFileViews.add(key);
  let state = fileStates.get(key);
  if (!state) {
    state = { state: "checking", bytes: file.bytes };
    fileStates.set(key, state);
    probeFile(fileURL(session.id, file.path)).then((next) => {
      fileStates.set(key, next);
      schedule();
    });
  }
  const fingerprint = `${state.state}|${state.bytes}|${file.bytes}|${file.path}|${file.callID}|${file.openPath}|${file.openScope}`;
  let view = fileViews.get(key);
  if (view?.fingerprint === fingerprint) return view.node;
  const node = createFileChip(document, file, state, {
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
  fileViews.set(key, { node, fingerprint });
  return node;
}

function speaker(name, agent = false) {
  const node = document.createElement("div");
  node.className = "chat-speaker";
  if (agent) {
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

function toolTick(entry, forceOpen = false) {
  if (!entry.args || typeof entry.args !== "object" || Array.isArray(entry.args)) throw new Error("tool arguments are missing or are not an object");
  usedToolViews.add(entry.key);
  let view = toolViews.get(entry.key);
  if (!view) {
    const root = document.createElement("div");
    const button = document.createElement("button");
    button.type = "button";
    button.className = "tool-tick";
    button.innerHTML = '<span class="tool-name"></span><span class="tool-key"></span><span class="tool-state"></span><span class="tool-ms"></span>';
    const pre = document.createElement("pre");
    pre.className = "tool-detail";
    const collapse = document.createElement("button");
    collapse.type = "button";
    collapse.className = "collapse-arrow";
    collapse.textContent = "↑";
    collapse.setAttribute("aria-label", `Collapse ${entry.name} tool`);
    collapse.onclick = () => { expanded.delete(entry.key); render(); };
    button.onclick = () => {
      expanded.has(entry.key) ? expanded.delete(entry.key) : expanded.add(entry.key);
      render();
    };
    root.append(button);
    view = { root, button, pre, collapse, args: null, result: null, content: null };
    toolViews.set(entry.key, view);
  }
  const open = forceOpen || expanded.has(entry.key);
  view.button.setAttribute("aria-expanded", String(open));
  const state = entry.result && typeof entry.result.ok === "boolean" ? (entry.result.ok ? "ok" : "error") : "";
  setText(view.button.children[0], `${open ? "▾" : "▸"} ${entry.name}`);
  setText(view.button.children[1], keyArgument(entry.args));
  setText(view.button.children[2], callServiceStatus(entry.name, entry.result) || state);
  view.button.children[2].className = `tool-state ${state === "error" ? "error" : ""}`;
  setText(view.button.children[3], formatDuration(entry.result?.ms));
  if (open) {
    if (!view.pre.textContent || view.args !== entry.args || view.result !== entry.result || view.content !== entry.content) {
      view.pre.textContent = `arguments\n${JSON.stringify(entry.args, null, 2)}\n\nresult\n${capResult(entry.content)}`;
    }
    if (!view.collapse.isConnected) view.root.append(view.collapse);
    if (!view.pre.isConnected) view.root.append(view.pre);
  } else if (view.pre.isConnected) {
    view.pre.remove();
    view.collapse.remove();
  }
  view.args = entry.args;
  view.result = entry.result;
  view.content = entry.content;
  return view.root;
}

function setText(node, value) {
  if (node.textContent !== value) node.textContent = value;
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
  if (!event || typeof event !== "object") throw new Error("notice event is missing or is not an object");
  const data = event.data && typeof event.data === "object" ? event.data : {};
  const content = document.createElement("div");
  content.className = "chat-content";
  if (event.type === "run.stopped") {
    const reason = (data.reason || "").replaceAll("_", " ");
		if (data.reason === "model_unreachable") {
			const line=document.createElement("span"); line.textContent=`model unreachable · ${entry.text || session.model_unreachable?.host || "model"}`; if(data.detail) line.title=data.detail; content.append(line);
		} else content.textContent = `stopped: ${reason}${data.detail ? ` · ${data.detail}` : ""}`;
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
  else if (event.type === "run.aborted") {
    const pathCount = Array.isArray(data.possibly_written_paths) ? data.possibly_written_paths.length : 0;
    content.textContent = `harness: ${String(data.reason || "aborted").replaceAll("_", " ")} at turn ${data.turn || 0}${pathCount ? ` · ${pathCount} possibly partial path${pathCount === 1 ? "" : "s"} unverified` : ""}`;
    content.classList.add("alarm");
  }
  else if (event.type === "message.queued") content.textContent = `queued (${data.position})`;
  else if (event.type === "model.retry") content.textContent = data.reason === "truncated_tool_call"
    ? `harness: retrying truncated ${data.tool || "tool"} call (${data.attempt || 1}/${data.max_attempts || 1})`
    : `harness: repaired malformed ${data.tool || "tool"} history and retried`;
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
  if (entry.agentRole === "c") {
    const label = document.createElement("span");
    label.className = "chat-notice-author";
    label.textContent = agentAuthor(session, "c");
    content.prepend(label);
  }
  return content;
}

function renderComposer(session) {
	document.body.classList.toggle("no-open-chats", !session);
  send.disabled = !session || !!store.replay;
  input.disabled = !session || !!store.replay;
  attachButton.disabled = !session || !!store.replay || attachmentsBusy;
  input.removeAttribute("placeholder");
  const queued = session?.queued_messages || 0;
  const state = session?.pending_approval || session?.pending_repo_policy || session?.pending_bind ? "waiting for you" : session?.run?.status || "idle";
  const unreachable = session?.model_unreachable;
  const busy = session?.model_busy;
  const operatorUntil = store.shell_identity?.operator_context ? `operator mode · until ${shortTime(store.shell_identity.operator_context_expires_at)}` : "";
  const queueText = queued ? `queued (${queued})${unreachable ? " · waiting for model" : ""}` : "";
  const message = [localNotice || (session && !session.runnable ? session.not_runnable_reason : unreachable ? `model unreachable · ${unreachable.host || "model"}` : busy ? `model busy · ${busy.host || "model"}` : state), queueText, operatorUntil].filter(Boolean).join(" · ");
  notice.textContent = message;
  notice.className = `chat-notice ${localAlarm || unreachable || (session && !session.runnable) ? "alarm" : ""}`;
	pendingFiles.replaceChildren(...queuedAttachments.map((file) => {
    const row = document.createElement("span");
    row.className = "chat-pending-file";
    row.textContent = `${file.path.split("/").pop()} · ${format(file.bytes)} B${file.reused ? " · reused" : ""}`;
    return row;
	}));
	pendingApproval.hidden = !(session?.pending_approval || session?.pending_repo_policy || session?.pending_bind);
	const policyCard = session?.pending_repo_policy ? createPolicyCard(session) : null;
	const bindCard = session?.pending_bind ? createBindCard(session) : null;
	pendingApproval.replaceChildren(...(session?.pending_approval ? [createApprovalCard(document, session.pending_approval, {
		replay: store.replay,
		decide: (callID, decision) => api("/api/approve", { session_id: session.id, call_id: callID, decision }),
	})] : policyCard ? [policyCard] : bindCard ? [bindCard] : []));
  renderStopState(stop, session, store.replay);
  retryModel.hidden = !unreachable;
  retryModel.disabled = !session || store.replay;
}

function createBindCard(session) {
	const card = document.createElement("section"); card.className = "approval-card workspace-bind-card";
	const title = document.createElement("span"); title.textContent = "Allow this";
	const heading = document.createElement("strong"); heading.textContent = `Bind this chat to ${session.pending_bind.dir}?`;
	const reason = document.createElement("span"); reason.textContent = "The directory named in your message is outside this chat's workspace.";
	const actions = document.createElement("div"); actions.className = "approval-actions";
	for (const [label, decision] of [["Yes", "yes"], ["No", "no"]]) {
		const button = document.createElement("button"); button.type = "button"; button.textContent = label;
		if (decision === "yes") { button.className = "default"; button.autofocus = true; }
		button.disabled = !!store.replay; button.onclick = () => void decideBind(session, decision); actions.append(button);
	}
	card.append(title, heading, reason, actions); return card;
}

async function decideBind(session, decision) {
	if (!session?.pending_bind || store.replay) return;
	try { await api("/api/bind", { session_id: session.id, decision }); reduce({ type: "snapshot", data: await api("/api/state", undefined, "GET") }); localNotice = ""; localAlarm = false; render(); }
	catch (error) { localNotice = error.message || String(error); localAlarm = true; renderComposer(store.sessions[selectedID()]); }
}

function createPolicyCard(session) {
	const policy = session.pending_repo_policy;
	const card = document.createElement("section"); card.className = "approval-card repo-policy-card";
	const title = document.createElement("span"); title.textContent = "Allow this";
	const heading = document.createElement("strong"); heading.textContent = "Trust this repo's policy?";
	const reason = document.createElement("span"); reason.textContent = "This repository wants to change defaults for this chat.";
	const detail = document.createElement("details"); const summary=document.createElement("summary");summary.textContent="Technical detail";
	const path = document.createElement("span"); path.textContent = `${policy.path}${policy.changed ? " · changed" : ""}`;
	const pre = document.createElement("pre"); pre.textContent = policy.error || `${policy.content}${policy.diff ? `\n\n${policy.diff}` : ""}`; detail.append(summary,path,pre);
	const actions = document.createElement("div"); actions.className = "approval-actions";
	for (const [label, action] of [["Yes, for this chat","policy-approve"],["Just once","policy-once"],["No","policy-deny"]]) { const button=document.createElement("button");button.type="button";button.textContent=label;if(action==="policy-approve"){button.className="default";button.autofocus=true}button.disabled=!!store.replay || (action!=="policy-deny" && !!policy.error);button.onclick=()=>void decidePolicy(session,action);actions.append(button) }
	card.append(title,heading,reason,detail,actions); return card;
}

async function decidePolicy(session, action) {
	const policy=session.pending_repo_policy; if(!policy||store.replay)return;
	try { await api(`/api/workspaces/${action}`,{dir:session.workspace_dir||session.workspace,hash:policy.hash,session_id:session.id}); reduce({type:"snapshot",data:await api("/api/state",undefined,"GET")}); localNotice="";localAlarm=false;render() }
	catch(error){localNotice=error.message||String(error);localAlarm=true;renderComposer(store.sessions[selectedID()])}
}

async function submit() {
  const session = store.sessions[selectedID()];
  if (!session || store.replay) return;
  const text = input.value.trim();
  if (!text && !queuedAttachments.length) return;
  try {
		await api("/api/message", { session_id: session.id, text, attachments: queuedAttachments.map(attachmentMetadata) });
    input.value = "";
    queuedAttachments = [];
    attachmentQueues.set(selectedID(), queuedAttachments);
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
  const session = store.sessions[selectedID()];
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
  if (store.replay || !store.sessions[selectedID()]) return;
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
      empty.textContent = "Attachments folder is empty";
      exchangeFileList.append(empty);
    }
  } catch (error) {
    localNotice = error.message || String(error);
    localAlarm = true;
    renderComposer(store.sessions[selectedID()]);
  }
}

async function queueExchangeFile(item) {
  const session = store.sessions[selectedID()];
  if (!session || !item) return;
  attachmentsBusy = true;
  localNotice = "Copying from attachments…";
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

send.onclick = submit;
stop.onclick = () => {
  const session = store.sessions[selectedID()];
  if (session && !store.replay) api("/api/stop", { session_id: session.id }).catch((error) => { localNotice = error.message; localAlarm = true; renderComposer(session); });
};
retryModel.onclick = async () => {
  const session = store.sessions[selectedID()];
  if (!session || store.replay) return;
  try { await api(`/api/servers/${encodeURIComponent(session.server_id || session.b_profile)}/probe`, { session_id: session.id, retry: true }); }
  catch (error) { localNotice = error.message; localAlarm = true; renderComposer(session); }
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
expandComposer.onclick = () => {
  composerExpanded = !composerExpanded;
  resize();
  input.focus();
};
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
    document.getElementById("shell-stop")?.click();
  } else if (event.key === "/" && !/INPUT|TEXTAREA|SELECT/.test(document.activeElement?.tagName || "")) {
    event.preventDefault();
    input.focus();
  }
});

function resize() {
  composer.classList.toggle("expanded", composerExpanded);
  expandComposer.textContent = composerExpanded ? "↧" : "↥";
  expandComposer.setAttribute("aria-label", composerExpanded ? "Collapse composer" : "Expand composer");
}
function busy(session) {
  return !!session && ["running", "queued", "paused", "stopping"].includes(session.run?.status);
}
function keyArgument(args) {
  if (!args || typeof args !== "object" || Array.isArray(args)) return "";
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
function shortTime(value) {
  const date = new Date(value || "");
  return Number.isNaN(date.getTime()) ? "soon" : date.toLocaleTimeString([], {hour:"2-digit", minute:"2-digit"});
}
