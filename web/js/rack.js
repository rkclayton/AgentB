import { api, store } from "./bus.js";
const root = document.getElementById("rack"),
  count = document.getElementById("tool-count");
export function renderRack() {
  const s = store.sessions[store.active];
  root.replaceChildren();
  if (!s) {
    count.textContent = "";
    return;
  }
  count.textContent = `${s.tools.length} available`;
  for (const tool of s.tools) {
    const row = document.createElement("div");
    row.className = `tool-row ${tool.enabled ? "" : "off"}`;
    const live = s._activeTool === tool.name,
      alarm = s._alarmTool === tool.name;
    row.innerHTML = `<span class="lamp ${live ? "live" : ""} ${alarm ? "alarm" : ""}"></span><span></span><span class="tool-calls number"></span><button class="switch ${tool.enabled ? "on" : ""}" type="button" aria-label="Toggle ${tool.name}"></button>`;
    row.children[1].textContent = tool.name;
    row.children[2].textContent = tool.calls || 0;
    row.querySelector("button").onclick = () =>
      api(`/api/tools/${tool.name}`, {
        session_id: s.id,
        enabled: !tool.enabled,
      });
    root.append(row);
  }
}
