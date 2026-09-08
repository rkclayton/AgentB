const activeStatuses = new Set(["running", "queued", "paused", "stopping"]);

export function projectStopState(session, replay = false) {
  const status = session?.run?.status || "idle";
  const stopping = !replay && status === "stopping";
  const active = !replay && !!session && activeStatuses.has(status);
  return {
    active,
    disabled: !active,
    state: stopping ? "stopping" : active ? "active" : "idle",
    label: stopping ? "Emergency stop — cancel immediately" : active ? "Stop active run — cancel and hold queued messages" : "No active run to stop",
  };
}

export function renderStopState(button, session, replay = false) {
  const state = projectStopState(session, replay);
  button.disabled = state.disabled;
  button.classList.toggle("active", state.state === "active");
  button.classList.toggle("stopping", state.state === "stopping");
  button.dataset.state = state.state;
  button.setAttribute("aria-label", state.label);
  button.setAttribute("title", state.label);
  return state;
}
