import { api, reduce, setActive, store, subscribe } from "./bus.js";

const sheet = document.getElementById("settings-sheet");
const gear = document.getElementById("settings");
const expanded = new Set();
const armed = new Set();
const drafts = new Map();
const errors = new Map();
const shownKeys = new Set();
let open = false;
let lastFocus = null;
let shellCredentialMessage = "";

export function initSettings() {
  gear.addEventListener("click", () => (open ? closeSheet() : openSheet()));
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && open) closeSheet();
  });
  sheet.addEventListener("click", click);
  sheet.addEventListener("focusout", blur);
  sheet.addEventListener("change", change);
  sheet.addEventListener("input", (event) => {
    if (event.target.matches(".setting-input[data-path]"))
      drafts.set(event.target.dataset.path, event.target.value);
  });
  subscribe((_state, event) => {
    if (
      open &&
      [
        "init",
        "snapshot",
        "active.changed",
        "config.changed",
		"shell.identity",
		"shell.credential",
        "server.probed",
        "session.created",
        "session.renamed",
        "session.closed",
        "session.reset",
        "memory.noted",
        "run.started",
        "run.stopped",
      ].includes(event.type)
    )
      render();
  });
}

function openSheet() {
  open = true;
  lastFocus = document.activeElement;
  sheet.classList.add("open");
  sheet.setAttribute("aria-hidden", "false");
  gear.setAttribute("aria-expanded", "true");
  render();
  requestAnimationFrame(() => sheet.querySelector("button, input")?.focus());
}

function closeSheet() {
  open = false;
  sheet.classList.remove("open");
  sheet.setAttribute("aria-hidden", "true");
  gear.setAttribute("aria-expanded", "false");
  (lastFocus || gear).focus();
}

function render() {
  const active = store.sessions[store.active];
  sheet.innerHTML = `
    <header class="settings-head"><span>Settings</span><button type="button" data-action="close" aria-label="Close settings">×</button></header>
    <div class="settings-scroll">
      ${group("Servers", servers())}
      ${group("Sessions", sessions())}
      ${group("Tools", tools(active))}
      ${group("Memory", memory(active))}
      ${group("Context", context(active))}
      ${group("Run", run())}
      ${group("Shell", shell())}
      ${group("Session", sessionControls(active))}
    </div>`;
}

function group(name, content) {
  return `<section class="settings-group"><h2>${name}</h2>${content}</section>`;
}

function servers() {
  const rows = store.servers
    .map((profile) => {
      const isOpen = expanded.has(profile.id);
      const reason = profileReason(profile);
      const failed = (profile.capabilities?.findings || []).some((x) =>
        x.startsWith("probe failed:"),
      );
      const lamp = profile._probing ? "live" : failed || reason ? "alarm" : "";
      return `<div class="profile ${isOpen ? "expanded" : ""}">
        <div class="profile-row">
          <button type="button" class="profile-summary" data-action="profile-toggle" data-id="${attr(profile.id)}">
            <span class="lamp ${lamp}"></span><span>${html(profile.label)}</span><span class="profile-url">${html(profile.base_url)}</span>
          </button>
          <button type="button" data-action="probe" data-id="${attr(profile.id)}">Test</button>
        </div>
        <div class="profile-expansion"><div class="profile-fields">
          ${profileFields(profile, reason)}
        </div></div>
      </div>`;
    })
    .join("");
  return `${rows}<button type="button" class="text-action" data-action="add-server">Add server</button>`;
}

function profileFields(profile, reason) {
  const id = profile.id;
  const p = `servers.${id}`;
  const caps = profile.capabilities || {};
  const efforts = profile.reasoning?.valid_efforts || caps.valid_efforts || [];
  const llama = caps.server === "llama.cpp";
  const sample = (name, title) => {
    const value = profile.sampling[name];
    return `<div class="settings-subhead">${title}</div>
      ${number(`${p}.sampling.${name}.temperature`, "temperature", value.temperature, "0.01")}
      ${number(`${p}.sampling.${name}.top_p`, "top_p", value.top_p, "0.01")}
      ${number(`${p}.sampling.${name}.top_k`, "top_k", value.top_k, "1", !llama, !llama ? "llama.cpp only" : "")}
      ${number(`${p}.sampling.${name}.min_p`, "min_p", value.min_p, "0.01", !llama, !llama ? "llama.cpp only" : "")}
      ${number(`${p}.sampling.${name}.presence_penalty`, "presence penalty", value.presence_penalty, "0.1")}
      ${number(`${p}.sampling.${name}.repeat_penalty`, "repeat penalty", value.repeat_penalty, "0.1", !llama, !llama ? "llama.cpp only" : "")}`;
  };
  const findings = (caps.findings || [])
    .map((value) => `<li>${html(value)}</li>`)
    .join("");
  return `${text(`${p}.label`, "label", profile.label)}
    ${text(`${p}.base_url`, "base_url", profile.base_url)}
    ${text(`${p}.model`, "model", profile.model)}
    ${secret(`${p}.api_key`, "api_key", profile.api_key, id)}
    ${number(`${p}.request_timeout_s`, "timeout", profile.request_timeout_s)}
    ${choices(`${p}.probe_mode`, "probe mode", ["full", "minimal", "off"], profile.probe_mode)}
    <p class="settings-note">minimal and off skip checks that spend tokens; assumed values are marked in findings</p>
    ${sample("thinking", "Sampling — thinking")}
    ${sample("nonthinking", "Sampling — nonthinking")}
    <div class="settings-subhead">Reasoning</div>
    ${choices(`${p}.reasoning.control`, "control", ["auto", "chat_template_kwargs", "top_level", "server_flag", "none"], profile.reasoning.control)}
    ${toggle(`${p}.reasoning.enabled`, "enabled", profile.reasoning.enabled)}
    ${efforts.length ? choices(`${p}.reasoning.effort`, "effort", efforts, profile.reasoning.effort) : row("effort", '<span class="settings-note inline">not supported by this server</span>')}
    ${toggle(`${p}.reasoning.preserve`, "preserve", profile.reasoning.preserve)}
    ${number(`${p}.context.reserve_output`, "reserve", profile.context.reserve_output)}
    ${number(`${p}.context.n_ctx_override`, caps.props ? "n_ctx override" : "n_ctx (required)", profile.context.n_ctx_override, "1", false, "", !caps.props && !profile.context.n_ctx_override)}
    ${textarea(`${p}.system_prompt_override`, "system prompt override", profile.system_prompt_override || "")}
    <p class="settings-note">variables: {{workspace}} {{tools}} {{memory}}</p>
    <div class="settings-subhead">Capabilities</div>
    <div class="findings"><span class="settings-note">${html(caps.probed_at || "not probed")}</span><ul>${findings || "<li>no findings</li>"}</ul></div>
    ${reason ? `<p class="field-error">${html(reason)}</p>` : ""}
    ${errors.get(p) ? `<p class="field-error">${html(errors.get(p))}</p>` : ""}
    <div class="settings-actions">
      <button type="button" data-action="duplicate-server" data-id="${attr(id)}">Duplicate</button>
      <button type="button" class="${armed.has(`server:${id}`) ? "confirm" : ""}" data-action="remove-server" data-id="${attr(id)}">${armed.has(`server:${id}`) ? "Confirm remove" : "Remove"}</button>
    </div>`;
}

function sessions() {
  const profiles = store.servers.filter((profile) => !profileReason(profile));
  const items = Object.values(store.sessions)
    .map((item) => {
      const profile = store.servers.find((x) => x.id === item.server_id);
      const running = item.run.status !== "idle";
      const key = `session:${item.id}`;
      return `<div class="session-row">
        <input class="session-label" data-session-label="${attr(item.id)}" value="${attr(item.label)}" aria-label="${attr(item.id)} label">
        <span>${html(profile?.label || item.server_id)}</span><span class="path" title="${attr(item.workspace)}">${html(item.workspace)}</span>
        <span>${html(item.run.status)}</span>
        <button type="button" class="${armed.has(key) ? "confirm" : ""}" data-action="close-session" data-id="${attr(item.id)}">${running && armed.has(key) ? "Confirm" : "Close"}</button>
      </div>${issue(`session.${item.id}`) ? `<p class="field-error">${html(issue(`session.${item.id}`))}</p>` : ""}`;
    })
    .join("");
  const options = profiles
    .map((p) => `<option value="${attr(p.id)}">${html(p.label)}</option>`)
    .join("");
  return `${items}
    <div class="settings-subhead">New session</div>
    ${row("label", '<input id="new-session-label" value="new session">')}
    ${row("profile", `<select id="new-session-profile">${options}</select>`)}
    ${row("workspace", `<input id="new-session-workspace" value="${attr(store.config.workspace || "")}">`)}
    <button type="button" class="text-action" data-action="new-session" ${options ? "" : "disabled"}>New session</button>
    ${issue("new-session") ? `<p class="field-error">${html(issue("new-session"))}</p>` : ""}
    ${number("run.max_concurrent", "max concurrent", store.config.run?.max_concurrent)}`;
}

function tools(active) {
  const costs = Object.fromEntries((active?.tools || []).map((tool) => [tool.name, tool.schema_tokens]));
  const head = (name) => `<div class="tool-setting-head"><code>${name}</code><span>${costs[name] ?? 0} tokens</span></div>`;
  const cfg = store.config;
  return `${head("read_file")}
    ${number("tools.read_file.default_limit", "default limit", cfg.tools?.read_file?.default_limit)}
    ${number("tools.read_file.max_limit", "max limit", cfg.tools?.read_file?.max_limit)}
    ${number("tools.read_file.max_line_chars", "max line chars", cfg.tools?.read_file?.max_line_chars)}
    ${head("list_dir")}
    ${number("tools.list_dir.max_entries", "max entries", cfg.tools?.list_dir?.max_entries)}
    ${text("tools.list_dir.ignore", "ignore", (cfg.tools?.list_dir?.ignore || []).join(", "), "list")}
    ${head("write_file")}${head("edit_file")}
    ${head("grep")}
    ${number("tools.grep.max_matches", "max matches", cfg.tools?.grep?.max_matches)}
    ${number("tools.grep.max_line_chars", "max line chars", cfg.tools?.grep?.max_line_chars)}
    ${head("shell")}
    ${number("shell.timeout_s", "timeout", cfg.shell?.timeout_s)}
    ${number("shell.max_timeout_s", "max timeout", cfg.shell?.max_timeout_s)}
    ${number("shell.max_output_lines_head", "head lines", cfg.shell?.max_output_lines_head)}
    ${number("shell.max_output_lines_tail", "tail lines", cfg.shell?.max_output_lines_tail)}
    ${text("shell.deny", "deny", (cfg.shell?.deny || []).join(", "), "list")}
    ${head("remember")}`;
}

function memory(active) {
  const value = (active?.memory_content || "")
    .split(/\r?\n/)
    .slice(0, 200)
    .join("\n");
  return `${toggle("memory.enabled", "enabled", store.config.memory?.enabled)}
    ${number("memory.max_tokens", "max tokens", store.config.memory?.max_tokens)}
    ${text("memory.dir", "directory", store.config.memory?.dir || "")}
    ${copyRow("file", active?.memory_path || "")}
    <pre class="memory-content">${html(value || "No notes for this workspace.")}</pre>`;
}

function context(active) {
  const profile = store.servers.find((x) => x.id === active?.server_id);
  const choice = store.config.context?.accounting || "auto";
  let actual = "estimated — no active profile";
  if (profile) {
    actual = choice === "estimated"
      ? "estimated — by choice"
      : profile.capabilities?.tokenize
        ? "exact — /tokenize available"
        : "estimated — no /tokenize on this profile";
  }
  const facts = store.serving_facts || {};
  const blocked = ["yes", "partial"].includes(facts.tokenize_blocks_on_slot);
  return `${number("context.soft_pct", "soft threshold (%)", Math.round((store.config.context?.soft_pct || 0) * 100), "1", false, "", false, "percent")}
    ${number("context.summary_pct", "summary threshold (%)", Math.round((store.config.context?.summary_pct || 0) * 100), "1", false, "", false, "percent")}
    ${choices("context.accounting", "accounting", ["auto", "exact", "estimated"], choice)}
    <p class="settings-note">${html(actual)}</p>
    ${blocked ? `<p class="settings-note">/tokenize measured ${html(facts.tokenize_busy_ms || "?")} ms busy and may occupy the generation slot</p>` : ""}
    ${row("reserve", `<output>${active?.budget?.reserve ?? 0}</output>`)}
    ${row("ceiling", `<output>${active?.budget?.ceiling ?? 0}</output>`)}
    <p class="settings-note">from profile ${html(profile?.label || "none")}</p>`;
}

function run() {
  const cfg = store.config;
  return `${number("run.max_turns", "max turns", cfg.run?.max_turns)}
    ${number("run.cycle_window", "cycle window", cfg.run?.cycle_window)}
    <p class="settings-note">0 = off</p>
    ${number("run.max_consecutive_tool_errors", "max tool errors", cfg.run?.max_consecutive_tool_errors)}
    <p class="settings-note">0 = off</p>
    ${choices("approval.mode", "approval mode", ["off", "mutating", "all"], cfg.approval?.mode)}
    ${number("run.queue_depth", "queue depth", cfg.run?.queue_depth)}`;
}

function shell() {
	const service = store.config.shell?.service_account || {};
	const credential = store.shell_credential || {};
	const stored = credential.stored
		? `stored ${credential.stored_at || "(time unavailable)"}`
		: "not stored";
  return `${text("shell.command", "command", (store.config.shell?.command || []).join(" "), "command")}
    ${toggle("shell.service_account.enabled", "service identity", service.enabled)}
    ${text("shell.service_account.account", "account", service.account || "agentb-svc")}
    ${text("shell.service_account.domain", "domain", service.domain || ".")}
    ${row("password", '<input id="shell-service-password" type="password" autocomplete="new-password" aria-label="Service-account password">')}
    <p class="settings-note">write-only credential: ${html(stored)}</p>
    <div class="settings-actions">
      <button type="button" data-action="store-shell-credential">Store</button>
      <button type="button" data-action="test-shell-credential">Test</button>
      <button type="button" data-action="clear-shell-credential">Clear</button>
    </div>
    ${shellCredentialMessage ? `<p class="settings-note">${html(shellCredentialMessage)}</p>` : ""}
    <p class="settings-note">changes apply to new shell calls</p>`;
}

function sessionControls(active) {
  if (!active) return '<p class="settings-note">No active session.</p>';
  const resetKey = `reset:${active.id}`;
  return `<div class="settings-actions vertical">
      <button type="button" class="${armed.has(resetKey) ? "confirm" : ""}" data-action="reset-session" data-id="${attr(active.id)}">${armed.has(resetKey) ? "Confirm reset" : "Reset session"}</button>
      <a class="text-button" href="/chat?session=${encodeURIComponent(active.id)}" target="_blank" rel="noopener">Open chat window</a>
    </div>
    ${copyRow("JSONL", active.log_path || "")}`;
}

function row(label, control, extra = "") {
  return `<div class="setting-row ${extra}"><label>${html(label)}</label><div>${control}</div></div>`;
}

function current(path, fallback) {
  return drafts.has(path) ? drafts.get(path) : fallback ?? "";
}

function issue(path) {
  for (const [field, message] of errors) {
    if (field === path || field.startsWith(`${path}.`) || path.startsWith(`${field}.`))
      return message;
  }
  return "";
}

function field(path, label, control, alarm = false) {
  const problem = issue(path);
  return `${row(label, control, alarm || problem ? "invalid" : "")}${problem ? `<p class="field-error">${html(problem)}</p>` : ""}`;
}

function text(path, label, value, kind = "text") {
  return field(path, label, `<input class="setting-input" data-path="${attr(path)}" data-kind="${kind}" value="${attr(current(path, value))}">`);
}

function number(path, label, value, step = "1", disabled = false, note = "", alarm = false, kind = "number") {
  const control = `<input class="setting-input number" type="number" step="${step}" data-path="${attr(path)}" data-kind="${kind}" value="${attr(current(path, value))}" ${disabled ? "disabled" : ""}>${note ? `<span class="control-note">${html(note)}</span>` : ""}`;
  return field(path, label, control, alarm);
}

function textarea(path, label, value) {
  return field(path, label, `<textarea class="setting-input" rows="8" data-path="${attr(path)}" data-kind="text">${html(current(path, value))}</textarea>`);
}

function secret(path, label, value, id) {
  const shown = shownKeys.has(id);
  const type = shown ? "text" : "password";
  return field(path, label, `<span class="secret-control"><input class="setting-input" type="${type}" data-path="${attr(path)}" data-kind="secret" value="${attr(current(path, value))}"><button type="button" data-action="show-key" data-id="${attr(id)}">${shown ? "hide" : "show"}</button></span>`);
}

function toggle(path, label, value) {
  return field(path, label, `<button type="button" role="switch" aria-checked="${!!value}" class="switch ${value ? "on" : ""}" data-action="config-toggle" data-path="${attr(path)}" data-value="${value ? "false" : "true"}"></button>`);
}

function choices(path, label, values, selected) {
  return field(path, label, `<span class="choice-row">${values.map((value) => `<button type="button" class="${value === selected ? "selected" : ""}" data-action="config-choice" data-path="${attr(path)}" data-value="${attr(value)}">${html(value)}</button>`).join("")}</span>`);
}

function copyRow(label, value) {
  return row(label, `<span class="copy-value"><code title="${attr(value)}">${html(value || "—")}</code><button type="button" data-action="copy" data-value="${attr(value)}">copy</button></span>`);
}

async function click(event) {
  const button = event.target.closest("[data-action]");
  if (!button) return;
  const action = button.dataset.action;
  const id = button.dataset.id;
  if (action === "close") return closeSheet();
  if (action === "profile-toggle") {
    expanded.has(id) ? expanded.delete(id) : expanded.add(id);
    return render();
  }
  if (action === "show-key") {
    shownKeys.has(id) ? shownKeys.delete(id) : shownKeys.add(id);
    return render();
  }
  if (action === "config-toggle" || action === "config-choice")
    return update(button.dataset.path, action === "config-toggle" ? button.dataset.value === "true" : button.dataset.value);
  if (action === "probe") {
    const profile = store.servers.find((x) => x.id === id);
    if (profile) profile._probing = true;
    render();
    try {
      await api(`/api/servers/${encodeURIComponent(id)}/probe`);
    } catch (error) {
      if (profile) profile._probing = false;
      errors.set(`servers.${id}`, error.message);
      render();
    }
    return;
  }
  if (action === "add-server") return addServer();
  if (action === "duplicate-server") return duplicateServer(id);
  if (action === "remove-server") return removeServer(id);
  if (action === "new-session") return newSession();
  if (action === "close-session") return closeSession(id);
  if (action === "reset-session") return resetSession(id);
	if (action === "store-shell-credential") return storeShellCredential();
	if (action === "test-shell-credential") return shellCredentialAction("test");
	if (action === "clear-shell-credential") return shellCredentialAction("clear");
  if (action === "copy") {
    if (button.dataset.value) await navigator.clipboard?.writeText(button.dataset.value);
  }
}

async function storeShellCredential() {
	const input = sheet.querySelector("#shell-service-password");
	const password = input?.value || "";
	if (input) input.value = "";
	try {
		const status = await api("/api/shell-credential", { action: "store", password });
		store.shell_credential = status;
		shellCredentialMessage = "credential stored";
	} catch (error) {
		shellCredentialMessage = error.message;
	}
	render();
}

async function shellCredentialAction(action) {
	try {
		const result = await api("/api/shell-credential", { action });
		if (action === "clear") {
			store.shell_credential = result;
			shellCredentialMessage = "credential removed";
		} else {
			store.shell_credential = result.credential || store.shell_credential;
			store.shell_identity = result.identity || store.shell_identity;
			shellCredentialMessage = result.message;
		}
	} catch (error) {
		shellCredentialMessage = error.message;
	}
	render();
}

async function blur(event) {
  const input = event.target;
  if (input.matches(".setting-input[data-path]")) {
    const path = input.dataset.path;
    if (input.dataset.kind === "secret" && input.value === "•••• set") return;
    let value = input.value;
    if (input.dataset.kind === "number") value = Number(value);
    if (input.dataset.kind === "percent") value = Number(value) / 100;
    if (input.dataset.kind === "list") value = value.split(",").map((x) => x.trim()).filter(Boolean);
    if (input.dataset.kind === "command") value = value.trim().split(/\s+/).filter(Boolean);
    drafts.set(path, input.value);
    await update(path, value);
  }
  if (input.matches("[data-session-label]")) {
    try {
      await api(`/api/sessions/${encodeURIComponent(input.dataset.sessionLabel)}`, { label: input.value });
    } catch (error) {
      errors.set(`session.${input.dataset.sessionLabel}`, error.message);
      render();
    }
  }
}

function change() {}

async function update(path, value) {
  try {
    const result = await api("/api/config", patch(path, value));
    drafts.delete(path);
    errors.delete(path);
    reduce({ type: "config.changed", data: { config: result } });
  } catch (error) {
    errors.set(error.field || path, error.message);
    render();
  }
}

function patch(path, value) {
  const parts = path.split(".");
  if (parts[0] === "servers") {
    const item = { id: parts[1] };
    assign(item, parts.slice(2), value);
    return { servers: [item] };
  }
  const result = {};
  assign(result, parts, value);
  return result;
}

function assign(target, parts, value) {
  let node = target;
  parts.forEach((part, index) => {
    if (index === parts.length - 1) node[part] = value;
    else node = node[part] = {};
  });
}

async function addServer() {
  const id = uniqueID("server");
  expanded.add(id);
  const result = await api("/api/config", { servers: [{ id, label: id, base_url: "http://127.0.0.1:8000", model: "model" }] });
  reduce({ type: "config.changed", data: { config: result } });
}

async function duplicateServer(id) {
  const source = store.servers.find((x) => x.id === id);
  if (!source) return;
  const copy = structuredClone(source);
  delete copy._probing;
  copy.id = uniqueID(`${id}-2`);
  copy.label = `${source.label} copy`;
  if (copy.api_key === "•••• set") copy.api_key = "";
  expanded.add(copy.id);
  const result = await api("/api/config", { servers: [copy] });
  reduce({ type: "config.changed", data: { config: result } });
}

function uniqueID(base) {
  let id = base;
  let suffix = 2;
  while (store.servers.some((profile) => profile.id === id)) id = `${base}-${suffix++}`;
  return id;
}

async function removeServer(id) {
  const key = `server:${id}`;
  if (!armed.has(key)) {
    armed.add(key);
    return render();
  }
  try {
    await api(`/api/servers/${encodeURIComponent(id)}`, undefined, "DELETE");
    armed.delete(key);
  } catch (error) {
    errors.set(`servers.${id}`, error.message);
    armed.delete(key);
    render();
  }
}

async function newSession() {
  const body = {
    label: sheet.querySelector("#new-session-label").value,
    server_id: sheet.querySelector("#new-session-profile").value,
    workspace: sheet.querySelector("#new-session-workspace").value,
  };
  try {
    const result = await api("/api/sessions", body);
    reduce({ type: "session.created", session_id: result.session.id, data: { session: result.session } });
    setActive(result.session.id);
  } catch (error) {
    errors.set("new-session", error.message);
    render();
  }
}

async function closeSession(id) {
  const item = store.sessions[id];
  const key = `session:${id}`;
  if (item?.run.status !== "idle" && !armed.has(key)) {
    armed.add(key);
    return render();
  }
  try {
    await api(`/api/sessions/${encodeURIComponent(id)}${armed.has(key) ? "?force=1" : ""}`, undefined, "DELETE");
    armed.delete(key);
  } catch (error) {
    errors.set(`session.${id}`, error.message);
    render();
  }
}

async function resetSession(id) {
  const key = `reset:${id}`;
  if (!armed.has(key)) {
    armed.add(key);
    return render();
  }
  try {
    await api(`/api/sessions/${encodeURIComponent(id)}/reset${store.sessions[id]?.run.status === "idle" ? "" : "?force=1"}`, {});
    armed.delete(key);
  } catch (error) {
    errors.set(`session.${id}`, error.message);
    render();
  }
}

function profileReason(profile) {
  const caps = profile.capabilities || {};
  const nctx = caps.n_ctx || profile.context?.n_ctx_override;
  if (!nctx) return "context length unknown";
  if (!caps.tool_calls) return "tool calling unavailable";
  if (caps.overflow_behavior === "truncate") return "server truncates context";
  if (!caps.streaming) return "streaming unavailable";
  if (store.config.context?.accounting === "exact" && !caps.tokenize)
    return "exact accounting requested but this server has no /tokenize";
  return "";
}

function html(value) {
  return String(value ?? "").replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[ch]);
}
function attr(value) {
  return html(value);
}
