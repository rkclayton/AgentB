import { api, setActive, store, subscribe } from "./bus.js";
import { renderTabs } from "./tabs.js";
import { renderRail } from "./rail.js";
import { renderFlow } from "./flow.js";
import { renderRack } from "./rack.js";
import { renderState } from "./state.js";
import { renderTimeline } from "./timeline.js";
import { initSettings } from "./settings.js";
const form = document.getElementById("composer"),
  input = document.getElementById("task"),
  chatLaunch = document.getElementById("chat-launch"),
  consoleLaunch = document.getElementById("console-launch"),
  stop = document.getElementById("stop");
const requestedSession = new URLSearchParams(location.search).get("session");
let initialSession = requestedSession;
let renderFrame = 0;
initSettings();
subscribe((_state, event) => {
  if (event.type === "snapshot" && initialSession && store.sessions[initialSession]) {
    const id = initialSession;
    initialSession = "";
    setActive(id);
    return;
  }
  // Streaming deltas are not displayed on Console. Rebuilding every panel for
  // each token can starve navigation while a model is responding.
  if (event.type === "model.delta") return;
  scheduleRender();
});

function scheduleRender() {
  if (renderFrame) return;
  renderFrame = requestAnimationFrame(renderConsole);
}

function renderConsole() {
  renderFrame = 0;
  renderTabs();
  renderRail();
  renderFlow();
  renderRack();
  renderState();
  renderTimeline();
	const identityAlarm = document.getElementById("shell-identity-alarm");
	const operatorContext = store.shell_identity?.operator_context;
	const identityUnavailable = store.shell_identity?.operator_approval_required || store.shell_identity?.fallback;
	identityAlarm.hidden = !operatorContext && !identityUnavailable;
	identityAlarm.textContent = operatorContext
		? "OPERATOR CONTEXT — tools are running with your Windows permissions"
		: identityUnavailable
			? `SERVICE IDENTITY UNAVAILABLE — shell requires explicit operator approval: ${store.shell_identity.reason}`
			: "";
  const s = store.sessions[store.active],
    busy = s && s.run.status !== "idle";
  const query = new URLSearchParams();
  if (s) query.set("session", s.id);
  const suffix = query.size ? `?${query}` : "";
  chatLaunch.href = `/chat${suffix}`;
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
