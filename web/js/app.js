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
  stop = document.getElementById("stop");
initSettings();
subscribe(() => {
  renderTabs();
  renderRail();
  renderFlow();
  renderRack();
  renderState();
  renderTimeline();
  const s = store.sessions[store.active],
    busy = s && s.run.status !== "idle";
  input.disabled = !!busy;
  input.placeholder =
    s?.run.status === "queued"
      ? "Queued"
      : busy
        ? "Run in progress"
        : "Send a task";
});
form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const s = store.sessions[store.active],
    text = input.value.trim();
  if (!s || !text) return;
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
