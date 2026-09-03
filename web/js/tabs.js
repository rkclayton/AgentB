import { api, setActive, store } from "./bus.js";

const root = document.getElementById("tabs");
const armed = new Set();

export function renderTabs() {
  root.replaceChildren();
  for (const value of Object.values(store.sessions)) {
    const profile = store.servers.find((item) => item.id === value.server_id);
    const tab = document.createElement("div");
    tab.className = `tab ${value.id === store.active ? "active" : ""} ${value.run.status} ${fault(value) ? "fault" : ""}`;
    tab.dataset.id = value.id;
    const ratio = value.budget?.ceiling
      ? Math.max(0, (value.budget.used_measured || value.budget.used_est) / value.budget.ceiling)
      : 0;
    const select = document.createElement("button");
    select.type = "button";
    select.className = "tab-select";
    select.innerHTML = `<span class="lamp"></span><span class="tab-label"></span><span class="tab-server"></span><span class="tab-status mono"></span><span class="tab-budget ${ratio > 0.85 ? "warn" : ""}" style="width:${Math.min(100, ratio * 100)}%"></span>`;
    tab.append(select);
    tab.querySelector(".tab-label").textContent = value.label;
    tab.querySelector(".tab-server").textContent = profile?.label || value.server_id;
    tab.querySelector(".tab-status").textContent = tabStatus(value);
    select.onclick = () => setActive(value.id);
    if (value.id !== "main" && !store.replay) {
      const close = document.createElement("button");
	  close.type = "button";
      close.className = `tab-close ${armed.has(value.id) ? "confirm" : ""}`;
      close.textContent = "×";
      close.setAttribute("aria-label", `Close ${value.label}`);
      close.onclick = (event) => closeTab(event, value);
      tab.append(close);
    }
    root.append(tab);
  }
  if (!store.replay) {
    const add = document.createElement("button");
    add.type = "button";
    add.className = "tab-add";
    add.textContent = "+";
    add.setAttribute("aria-label", "Open session settings");
    add.onclick = () => {
      document.getElementById("settings").click();
      requestAnimationFrame(() =>
        [...document.querySelectorAll(".settings-group h2")]
          .find((heading) => heading.textContent === "Sessions")
          ?.scrollIntoView({ block: "start" }),
      );
    };
    root.append(add);
  }
}

function tabStatus(value) {
  if (!value.runnable) return value.not_runnable_reason;
  const run = value.run;
  if (run.status === "running") return `running ${run.turn}/${run.max_turns}`;
  if (run.status === "queued") return `queued ${run.queue_position}`;
  return run.status || "idle";
}
function fault(value) {
  return (
    !value.runnable ||
    ["model_error", "cycle", "tool_errors", "context_ceiling", "profile_not_runnable"].includes(value._lastStop)
  );
}
async function closeTab(event, value) {
  event.preventDefault();
  event.stopPropagation();
  if (value.run.status !== "idle" && !armed.has(value.id)) {
    armed.add(value.id);
    renderTabs();
    return;
  }
  await api(`/api/sessions/${value.id}${armed.has(value.id) ? "?force=1" : ""}`, undefined, "DELETE");
  armed.delete(value.id);
}
