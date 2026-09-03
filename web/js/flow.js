import { store } from "./bus.js";

const root = document.getElementById("flow");
const count = document.getElementById("flow-count");
let renderedSession = "";
let renderedStage = "";
let moving = false;
let finishTimer = 0;

export function renderFlow(force = false) {
  const session = store.sessions[store.active];
  if (!session || (!session.run.run_id && !(session.messages || []).length)) {
    clearTimeout(finishTimer);
    moving = false;
    renderedSession = session?.id || "";
    renderedStage = "";
    root.innerHTML = `<div class="idle"><img src="/static/assets/idle.svg" alt=""><p>Send a task to start the loop.</p></div>`;
    count.textContent = "";
    return;
  }
  const stages = store.flow.stages || [];
  const points = layout(stages);
  const stage = session._stage || stages[0] || "";
  const reduced = matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (!force && moving && renderedSession === session.id) return;
  if (!force && !reduced && renderedSession === session.id && renderedStage && stage && stage !== renderedStage) {
    const from = points[Math.max(0, stages.indexOf(renderedStage))] || points[0];
    const to = points[Math.max(0, stages.indexOf(stage))] || from;
    draw(session, stages, points, renderedStage, from);
    const dot = root.querySelector(".travel-dot");
    moving = true;
    requestAnimationFrame(() => {
      dot.style.transform = `translate(${to.x - from.x}px, ${to.y - from.y}px)`;
    });
    clearTimeout(finishTimer);
    finishTimer = setTimeout(() => {
      moving = false;
      renderedStage = stage;
      renderFlow(true);
    }, 260);
  } else {
    draw(session, stages, points, stage, points[Math.max(0, stages.indexOf(stage))] || points[0]);
    renderedStage = stage;
  }
  renderedSession = session.id;
  count.textContent =
    session.run.status === "running" || session.run.status === "paused"
      ? `turn ${session.run.turn}/${session.run.max_turns}`
      : session.run.status;
}

function draw(session, stages, points, activeStage, dotPoint) {
  const lines = [];
  for (let index = 1; index < points.length; index++)
    lines.push(`<path class="wire" d="M${points[index - 1].x} ${points[index - 1].y} L${points[index].x} ${points[index].y}"/>`);
  const nodes = stages
    .map((name, index) => {
      const point = points[index];
      const paused = session.run.status === "paused" && name === "dispatch";
      const active = activeStage === name || paused;
      const alarm = name === "dispatch" && session._dispatchAlarm;
      const done = (session._completedStages || []).includes(name);
      const readout = active ? readoutFor(session, name) : "";
      return `<g class="node ${active ? "active" : ""} ${alarm ? "alarm" : ""} ${done ? "done" : ""} ${name.replaceAll("_", "-")}" transform="translate(${point.x} ${point.y})"><rect class="node-lamp" width="8" height="8" rx="1"/><text x="14" y="8">${label(name)}</text>${readout ? `<text class="readout" x="14" y="26">${readout}</text>` : ""}</g>`;
    })
    .join("");
  root.innerHTML = `<svg viewBox="0 0 760 220" role="img" aria-label="Agent flow">${lines.join("")}<circle class="travel-dot" cx="${dotPoint.x + 4}" cy="${dotPoint.y + 4}" r="3"/>${nodes}</svg>`;
}

function layout(stages) {
  const points = [];
  const top = Math.min(5, stages.length);
  for (let index = 0; index < top; index++) points.push({ x: 30 + index * 145, y: 48 });
  for (let index = top; index < stages.length; index++) points.push({ x: 610 - (index - top) * 190, y: 156 });
  return points;
}
function readoutFor(session, name) {
  if (name !== "call_model") return "";
  const progress = session._progress;
  if (progress?.total) return `prefill ${progress.cache || progress.processed || 0} / ${progress.total}`;
  const rate = session._timings?.predicted_per_second;
  return rate ? `${Math.round(rate)} tok/s` : "";
}
const label = (name) => name.replaceAll("_", " ");
