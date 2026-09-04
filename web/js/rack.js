import { api, store } from "./bus.js";
const root = document.getElementById("rack"),
  count = document.getElementById("tool-count");
const labels = {
  read_file: "Read file",
  list_dir: "List folder",
  write_file: "Write file",
  edit_file: "Edit file",
  grep: "Search text",
  shell: "Shell",
  remember: "Remember",
  recall: "Recall",
  glob: "Find files",
};
export function renderRack() {
  const s = store.sessions[store.active];
  root.replaceChildren();
  if (!s) {
    count.textContent = "";
    return;
  }
  count.textContent = String(s.tools.length);
  for (const tool of s.tools) {
    const row = document.createElement("div");
    row.className = `tool-row ${tool.enabled ? "" : "off"}`;
    const live = s._activeTool === tool.name,
      alarm = s._alarmTool === tool.name;
    row.innerHTML = `<span class="lamp ${live ? "live" : ""} ${alarm ? "alarm" : ""}"></span><span></span><span class="tool-calls number"></span><button class="switch ${tool.enabled ? "on" : ""}" type="button" aria-label="Toggle ${tool.name}"></button>`;
    row.children[1].textContent = labels[tool.name] || tool.name.replaceAll("_", " ");
    row.children[2].textContent = tool.calls || 0;
    row.querySelector("button").onclick = () =>
      api(`/api/tools/${tool.name}`, {
        session_id: s.id,
        enabled: !tool.enabled,
      });
    root.append(row);
  }
}
