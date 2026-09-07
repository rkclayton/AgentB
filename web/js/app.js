import { api, setActive, store, subscribe } from "./bus.js";
import { initShell } from "./shell.js";
import { renderRail } from "./rail.js";
import { renderFlow } from "./flow.js";
import { renderRack } from "./rack.js";
import { renderState } from "./state.js";
import { renderTimeline } from "./timeline.js";
import { initSettings } from "./settings.js";
import { createMessageDropController } from "./message-drop.js";
import { createApprovalCard } from "./approval.js";
const dropLastMessage = document.getElementById("drop-last-message");
const requestedSession = new URLSearchParams(location.search).get("session");
let initialSession = requestedSession;
let renderFrame = 0;
initShell({ page: "console", reportError: showError });
const dropControl = createMessageDropController(dropLastMessage, {
  session: () => store.sessions[store.active],
  interactive: () => !store.replay,
  confirmDrop: (message) => window.confirm(message),
  drop: (id) => api(`/api/sessions/${encodeURIComponent(id)}/messages/drop-last`, {}),
  reportError: showError,
});
initSettings();
subscribe((_state, event) => {
  if (event.type === "snapshot" && initialSession && store.sessions[initialSession]) {
    const id = initialSession;
    initialSession = "";
    setActive(id);
    return;
  }
  // Incremental text projection changes only the live Activity readout on Console.
  // Chat consumes the same patch independently; avoid rebuilding History/State.
  if (event.type === "projection.patch" && (event.data?.operations || []).some((operation) =>
    operation.path === "/run/partial" || /^\/chat\/[^/]+\/(reasoning|text)$/.test(operation.path))) {
    scheduleFlowRender();
    return;
  }
  scheduleRender();
});

function scheduleRender() {
  if (renderFrame) return;
  renderFrame = requestAnimationFrame(renderConsole);
}
let flowFrame = 0;
function scheduleFlowRender() {
  if (flowFrame) return;
  flowFrame = requestAnimationFrame(() => {
    flowFrame = 0;
    renderFlow();
  });
}
setInterval(() => {
  const session = store.sessions[store.active];
  if (session && session.run.status === "running" && session.activity?.stage === "call_model")
    scheduleFlowRender();
}, 1000);

function renderConsole() {
  renderFrame = 0;
  renderRail();
  renderFlow();
  renderRack();
  renderState();
  renderTimeline();
  placeDropLastMessage();
  dropControl.render();
  const s = store.sessions[store.active];
	renderPendingApproval(s);
}

function placeDropLastMessage() {
  const heads = document.querySelectorAll(".timeline-model > .timeline-head");
  const target = heads[heads.length - 1];
  dropLastMessage.hidden = !target;
  if (target) target.append(dropLastMessage);
}

function renderPendingApproval(session) {
	const root = document.getElementById("console-pending-approval");
	root.hidden = !session?.pending_approval;
	root.replaceChildren(...(session?.pending_approval ? [createApprovalCard(document, session.pending_approval, {
		replay: store.replay,
		decide: (callID, decision) => api("/api/approve", { session_id: session.id, call_id: callID, decision }),
	})] : []));
}
document.addEventListener("keydown", (event) => {
  if (event.ctrlKey && /^[1-9]$/.test(event.key)) {
    const id = Object.keys(store.sessions)[Number(event.key) - 1];
    if (id) {
      event.preventDefault();
      setActive(id);
    }
  }
});
function showError(message) {
  const node = document.getElementById("connection");
  node.textContent = message;
  node.className = "alarm";
}
