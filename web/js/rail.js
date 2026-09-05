import { store } from "./bus.js";
import { percentClass } from "./percent.js";
const root = document.getElementById("rail"),
  order = [
    "system",
    "memory",
    "tools",
    "history",
    "files",
    "results",
    "fetched",
    "summary",
  ];
export function renderRail() {
  const s = store.sessions[store.active];
  if (!s) {
    root.replaceChildren();
    return;
  }
  const b = s.budget || {},
    n = Math.max(1, b.n_ctx || 1),
    used = b.used_measured || b.used_est || 0,
    ratio = used / Math.max(1, b.ceiling || 1),
    estimated = new Set(b.estimated_categories || []);
  const meter = document.createElement("div");
  meter.className = `meter ${ratio > 0.85 ? "warn" : ""} ${ratio > 1 ? "over" : ""} ${s._compacted ? "compacted" : ""}`;
  const labels = document.createElement("div");
  labels.className = "rail-labels";
  for (const name of order) {
    const value = b.categories?.[name] || 0,
      wide = (value / n) * 100,
      segment = document.createElement("span");
    segment.className = `segment ${percentClass(wide)} ${estimated.has(name) ? "estimated" : ""}`;
    segment.title = `${name} ${estimated.has(name) ? "~" : ""}${number(value)}`;
    meter.append(segment);
    const label = document.createElement("span");
    label.className = `rail-label ${percentClass(wide)}`;
    label.textContent = wide > 5 ? name : "";
    labels.append(label);
  }
  const reserve = document.createElement("span");
  reserve.className = `reserve ${percentClass(((b.reserve || 0) / n) * 100)}`;
  meter.append(reserve);
  const tick = document.createElement("span");
  tick.className = `ceiling-tick ${percentClass(((b.ceiling || 0) / n) * 100)}`;
  meter.append(tick);
  const edge = document.createElement("span");
  edge.className = `fill-edge ${percentClass((used / n) * 100)}`;
  meter.append(edge);
  const readout = document.createElement("div");
  readout.className = "rail-readout";
  const primary = document.createElement("span");
  primary.textContent = `${b.estimated ? "~" : ""}${number(used)} / ${number(b.ceiling || 0)}`;
  readout.append(primary);
  if (b.used_measured) {
    const drift = document.createElement("span");
    drift.className = "rail-drift";
    drift.textContent = ` ${signed(b.drift || 0)}`;
    readout.append(drift);
  }
  const profile = store.servers.find((x) => x.id === s.server_id);
  if (
    profile?.capabilities?.cached_tokens &&
    b.cached_last !== null &&
    b.cached_last !== undefined
  )
    readout.append(`  cached ${number(b.cached_last)}`);
  const caption = document.createElement("div");
  caption.className = "rail-caption";
  caption.textContent =
    b.mode === "estimated" ? "context (estimated)" : "context";
  root.replaceChildren(meter, readout, labels, caption);
}
const number = (value) => new Intl.NumberFormat().format(value || 0);
const signed = (value) =>
  `${value < 0 ? "−" : value > 0 ? "+" : "±"}${number(Math.abs(value))}`;
