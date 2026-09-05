import { store } from "./bus.js";
import { liveTelemetry, recordedTelemetry } from "./telemetry.js";

const root = document.getElementById("flow");
const count = document.getElementById("flow-count");
const labels = {
  assemble: "Prepare",
  call_model: "Model",
  parse: "Read response",
  dispatch: "Choose action",
  execute: "Run tool",
  append: "Update context",
  compact: "Compact",
  wait_user: "Wait",
};

export function renderFlow() {
  const session = store.sessions[store.active];
  const stages = store.flow.stages || [];
  root.replaceChildren();
  for (const name of stages) root.append(stageRow(session, name));
  if (!session) {
    count.textContent = "";
    return;
  }
  count.textContent =
    session.run.status === "running" || session.run.status === "paused"
      ? `${session.run.turn}/${session.run.max_turns}`
      : session.run.status;
}

function stageRow(session, name) {
  const row = document.createElement("div");
  const active = !!session && (session._stage === name || (session.run.status === "paused" && name === "dispatch"));
  const alarm = !!session && name === "dispatch" && session._dispatchAlarm;
  const done = !!session && (session._completedStages || []).includes(name);
  row.className = `activity-row ${active ? "active" : ""} ${done ? "done" : ""} ${alarm ? "alarm" : ""}`;

  const lamp = document.createElement("span");
  lamp.className = "activity-lamp";
  const label = document.createElement("span");
  label.textContent = labels[name] || friendly(name);
  const readout = document.createElement("span");
  readout.className = "activity-readout";
  readout.textContent = active || (store.replay && name === "call_model")
    ? readoutFor(session, name)
    : "";
  row.append(lamp, label, readout);
  return row;
}

function readoutFor(session, name) {
  if (name !== "call_model") return "";
  if (store.replay) {
    const recorded = recordedTelemetry(session);
    if (!recorded) return "recorded";
    return `recorded · think ${number(recorded.reasoningTokens)}${recorded.rate === null ? "" : ` · ${recorded.rate} tok/s`}`;
  }
  const telemetry = liveTelemetry(session);
  if (telemetry) {
    if (!telemetry.hasChunk)
      return `waiting ${telemetry.ageSeconds}s · think ~0 · rate —`;
    const progress = session?._progress;
    if (telemetry.rate === null && telemetry.reasoningTokens === 0 && progress?.total)
      return `prefill ${number(progress.cache || progress.processed || 0)}/${number(progress.total)} · chunk ${telemetry.ageSeconds}s`;
    const rate = telemetry.rate === null ? "rate —" : `~${telemetry.rate} tok/s`;
    return `chunk ${telemetry.ageSeconds}s · think ~${number(telemetry.reasoningTokens)} · ${rate}`;
  }
  const progress = session?._progress;
  if (progress?.total) return `${progress.cache || progress.processed || 0} / ${progress.total}`;
  return "";
}

const number = (value) => Number(value || 0).toLocaleString("en-US");

function friendly(value) {
  const text = String(value || "").replaceAll("_", " ");
  return text ? text[0].toUpperCase() + text.slice(1) : "";
}
