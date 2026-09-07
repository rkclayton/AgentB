const activeStatuses = new Set(["running", "queued", "paused", "stopping"]);

export function projectStopState(session, replay = false) {
  const active = !replay && !!session && activeStatuses.has(session.run?.status);
  return {
    active,
    disabled: !active,
    label: active ? "Stop active run" : "No active run to stop",
  };
}

export function renderStopState(button, session, replay = false) {
  const state = projectStopState(session, replay);
  button.disabled = state.disabled;
  button.classList.toggle("active", state.active);
  button.dataset.state = state.active ? "active" : "idle";
  button.setAttribute("aria-label", state.label);
  button.title = state.label;
  return state;
}
