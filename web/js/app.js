import { api, setActive, store, subscribe } from "./bus.js";
import { renderTabs } from "./tabs.js";
import { renderRail } from "./rail.js";
import { renderFlow } from "./flow.js";
import { renderRack } from "./rack.js";
import { renderState } from "./state.js";
import { renderTimeline } from "./timeline.js";
import { initSettings } from "./settings.js";
import { createOperatorStatusController, isOperatorStateEvent } from "./operator-status.js";
const form = document.getElementById("composer"),
  input = document.getElementById("task"),
  consoleLaunch = document.getElementById("console-launch"),
  operatorStatus = document.getElementById("operator-status"),
  stop = document.getElementById("stop");
const requestedSession = new URLSearchParams(location.search).get("session");
let initialSession = requestedSession;
let renderFrame = 0;
const operatorControl = createOperatorStatusController(operatorStatus, {
  identity: () => store.shell_identity,
  interactive: () => !store.replay,
  confirmEnable: () => window.confirm("Enable operator mode? Tools will run as your Windows account until you turn it off or it expires."),
  setOperatorContext: (enabled) => api("/api/config", { shell: { operator_context: enabled } }),
  reportError: showError,
});
initSettings();
subscribe((_state, event) => {
  if (isOperatorStateEvent(event)) operatorControl.render();
  if (event.type === "snapshot" && initialSession && store.sessions[initialSession]) {
    const id = initialSession;
    initialSession = "";
    setActive(id);
    return;
  }
  // Stream traffic only changes the Activity readout. Avoid rebuilding History
  // and State rows while the model is producing tokens or prompt progress.
  if (event.type === "model.delta" || event.type === "model.progress") {
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
  if (session && session.run.status === "running" && session._stage === "call_model")
    scheduleFlowRender();
}, 1000);

function renderConsole() {
  renderFrame = 0;
  renderTabs();
  renderRail();
  renderFlow();
  renderRack();
  renderState();
  renderTimeline();
	const identityAlarm = document.getElementById("shell-identity-alarm");
	const identityUnavailable = store.shell_identity?.operator_approval_required || store.shell_identity?.fallback;
	operatorControl.render();
	identityAlarm.hidden = !identityUnavailable;
	identityAlarm.textContent = identityUnavailable
		? `SERVICE IDENTITY UNAVAILABLE — shell requires explicit operator approval: ${store.shell_identity.reason}`
		: "";
  const s = store.sessions[store.active],
    busy = s && s.run.status !== "idle";
  const query = new URLSearchParams();
  if (s) query.set("session", s.id);
  const suffix = query.size ? `?${query}` : "";
  consoleLaunch.href = `/${suffix}`;
  input.disabled = !!busy;
	stop.hidden = !!store.replay;
	document.getElementById("mode").textContent = store.replay ? "replay" : "";
  input.placeholder =
	store.replay
	  ? "Replay"
	  : s?.run.status === "queued"
      ? "Queued"
      : busy
        ? "Run in progress"
        : "Send a task";
}
form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const s = store.sessions[store.active],
    text = input.value.trim();
  if (!s || !text || store.replay) return;
  input.value = "";
  try {
    await api("/api/message", { session_id: s.id, text });
  } catch (error) {
    input.value = text;
    showError(error.message);
  }
});
stop.onclick = (event) => {
  const s = store.sessions[store.active];
  if (s)
    api(
      "/api/stop",
      event.shiftKey ? { all: true } : { session_id: s.id },
    ).catch((error) => showError(error.message));
};
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
