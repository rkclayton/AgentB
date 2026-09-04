import { api, reduce, setActive, store, subscribe } from "./bus.js";

const sheet = document.getElementById("settings-page");
const gear = document.getElementById("settings");
const consoleLaunch = document.getElementById("console-launch");
const expanded = new Set();
const armed = new Set();
const drafts = new Map();
const draftKinds = new Map();
const errors = new Map();
const shownKeys = new Set();
let open = false;
let lastFocus = null;
let shellCredentialMessage = "";
let shellCredentialAlarm = false;
let serviceAccountStatus = { loaded: false, supported: true, exists: false, administrator: false };
let serviceAccountBusy = false;
let serviceAccountMessage = "";
let serviceAccountAlarm = false;
let hardeningStatus = { loaded: false, supported: true, applied: false };
let hardeningBusy = false;
let hardeningMessage = "";
let hardeningAlarm = false;
let settingsSaving = false;
let settingsSaveMessage = "All changes saved";
let settingsSaveAlarm = false;
let activeSection = "servers";
let hardeningServerID = "";

const sectionLabels = [
  ["servers", "Connections"],
  ["sessions", "Sessions"],
  ["tools", "Tools"],
  ["memory", "Memory"],
  ["context", "Context"],
  ["run", "Run & approval"],
  ["shell", "Security"],
  ["session", "Current session"],
];

export function initSettings() {
  gear.addEventListener("click", () => (open ? closeSettings() : openSettings()));
  consoleLaunch.addEventListener("click", (event) => {
    event.preventDefault();
    if (open) closeSettings();
  });
  document.addEventListener("settings.open", (event) => openSettings(event.detail?.section));
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && open) closeSettings();
    if (open && (event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") {
      event.preventDefault();
      saveSettings();
    }
  });
  sheet.addEventListener("click", click);
  sheet.addEventListener("focusout", blur);
  sheet.addEventListener("change", change);
  sheet.addEventListener("input", (event) => {
    if (event.target.matches(".setting-input[data-path]")) {
      drafts.set(event.target.dataset.path, event.target.value);
      draftKinds.set(event.target.dataset.path, event.target.dataset.kind || "text");
      settingsSaveMessage = "Unsaved changes";
      settingsSaveAlarm = false;
      refreshSaveControls();
    }
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
        "session.updated",
        "session.closed",
        "session.reset",
        "memory.noted",
        "run.started",
        "run.stopped",
      ].includes(event.type)
    )
      render();
    if (open && event.type === "snapshot") {
      refreshServiceAccountStatus();
      refreshHardeningStatus();
    }
  });
  const requested = location.hash.match(/^#settings(?:\/([a-z-]+))?$/);
  if (requested) requestAnimationFrame(() => openSettings(requested[1] || "servers"));
}

function openSettings(section = "") {
  if (sectionLabels.some(([id]) => id === section)) activeSection = section;
  open = true;
  lastFocus = document.activeElement;
  sheet.hidden = false;
  sheet.setAttribute("aria-hidden", "false");
  gear.setAttribute("aria-expanded", "true");
  gear.setAttribute("aria-label", "Close settings");
  gear.setAttribute("aria-pressed", "true");
  gear.classList.add("selected");
  consoleLaunch.classList.remove("selected");
  consoleLaunch.removeAttribute("aria-current");
  history.replaceState(null, "", `#settings/${activeSection}`);
  render();
  refreshServiceAccountStatus();
	refreshHardeningStatus();
  requestAnimationFrame(() => sheet.querySelector(".settings-nav button.selected")?.focus());
}

function closeSettings() {
  open = false;
  sheet.hidden = true;
  sheet.setAttribute("aria-hidden", "true");
  gear.setAttribute("aria-expanded", "false");
  gear.setAttribute("aria-label", "Settings");
  gear.setAttribute("aria-pressed", "false");
  gear.classList.remove("selected");
  consoleLaunch.classList.add("selected");
  consoleLaunch.setAttribute("aria-current", "page");
  history.replaceState(null, "", `${location.pathname}${location.search}`);
  (lastFocus || gear).focus();
}

function render() {
  const active = store.sessions[store.active];
  const scrollTop = sheet.querySelector(".settings-content")?.scrollTop || 0;
  const focusKey = controlKey(document.activeElement);
  const secretValues = new Map(
    [...sheet.querySelectorAll('input[type="password"]')]
      .map((input) => [controlKey(input), input.value])
      .filter(([key, value]) => key && value),
  );
  const content = {
    servers: () => servers(),
    sessions: () => sessions(),
    tools: () => tools(active),
    memory: () => memory(active),
    context: () => context(active),
    run: () => run(),
    shell: () => shell(active),
    session: () => sessionControls(active),
  };
  const label = sectionLabels.find(([id]) => id === activeSection)?.[1] || "Settings";
  const saveLabel = settingsSaving ? "Saving…" : drafts.size ? `Save (${drafts.size})` : "Saved";
  sheet.innerHTML = `
    <header class="settings-head">
      <div><strong>Settings</strong><span data-save-status class="${settingsSaveAlarm ? "alarm" : ""}">${html(settingsSaveMessage)}</span></div>
      <div class="settings-head-actions"><button type="button" class="settings-save" data-action="save-settings" ${settingsSaving || !drafts.size ? "disabled" : ""}>${saveLabel}</button><button type="button" data-action="close" aria-label="Close settings">×</button></div>
    </header>
    <div class="settings-layout">
      <nav class="settings-nav" aria-label="Settings sections">
        ${sectionLabels.map(([id, name]) => `<button type="button" class="${id === activeSection ? "selected" : ""}" data-action="settings-section" data-id="${id}" aria-current="${id === activeSection ? "page" : "false"}">${name}</button>`).join("")}
      </nav>
      <div class="settings-content" tabindex="-1">${group(label, content[activeSection]())}</div>
    </div>`;
  const contentNode = sheet.querySelector(".settings-content");
  contentNode.scrollTop = scrollTop;
  for (const input of sheet.querySelectorAll('input[type="password"]')) {
    const value = secretValues.get(controlKey(input));
    if (value) input.value = value;
  }
  const focusNode = [...sheet.querySelectorAll("button, input, textarea, select, summary")]
    .find((node) => controlKey(node) === focusKey);
  focusNode?.focus({ preventScroll: true });
}

function refreshSaveControls() {
  const status = sheet.querySelector("[data-save-status]");
  const button = sheet.querySelector('[data-action="save-settings"]');
  if (status) {
    status.textContent = settingsSaveMessage;
    status.classList.toggle("alarm", settingsSaveAlarm);
  }
  if (button) {
    button.textContent = settingsSaving ? "Saving…" : drafts.size ? `Save (${drafts.size})` : "Saved";
    button.disabled = settingsSaving || !drafts.size;
  }
}

function controlKey(node) {
  if (!node || !sheet.contains(node)) return "";
  return node.id || node.dataset?.path || [node.dataset?.action, node.dataset?.id || node.dataset?.setupAction].filter(Boolean).join(":") || node.getAttribute?.("aria-label") || "";
}

function group(name, content) {
  return `<section class="settings-group"><h2>${name}</h2>${content}</section>`;
}

function servers() {
  const rows = store.servers
    .map((profile) => {
      const isOpen = expanded.has(profile.id);
      const hasPendingChanges = [...drafts.keys()].some((path) => path.startsWith(`servers.${profile.id}.`));
      const reason = profileReason(profile);
      const failed = (profile.capabilities?.findings || []).some((x) =>
        x.startsWith("probe failed:"),
      );
      const testState = profile._probing
        ? "testing"
        : failed
          ? "failed"
          : !reason && profile.capabilities?.probed_at
            ? "ready"
            : "not tested";
      const ready = !reason && !!profile.capabilities?.probed_at;
      const lamp = failed || reason ? "alarm" : profile._probing || ready ? "live" : "";
      return `<div class="profile ${isOpen ? "expanded" : ""}">
        <div class="profile-row">
          <button type="button" class="profile-summary" data-action="profile-toggle" data-id="${attr(profile.id)}">
            <span class="lamp ${lamp}"></span><span>${html(profile.label)}</span><span class="profile-url">${html(profile.base_url)}</span><span class="profile-state">${testState}</span>
          </button>
          <button type="button" data-action="probe" data-id="${attr(profile.id)}" title="${hasPendingChanges ? "Save this connection before testing it." : ""}" ${profile._probing || hasPendingChanges ? "disabled" : ""}>${profile._probing ? "Testing…" : "Test"}</button>
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
      const running = item.run.status !== "idle";
      const key = `session:${item.id}`;
      const profileOptions = store.servers
        .map((candidate) => {
          const problem = profileReason(candidate);
          return `<option value="${attr(candidate.id)}" ${candidate.id === item.server_id ? "selected" : ""} ${problem ? "disabled" : ""}>${html(candidate.label)}</option>`;
        })
        .join("");
      return `<div class="session-row">
        <input class="session-label" data-session-label="${attr(item.id)}" value="${attr(item.label)}" aria-label="${attr(item.id)} label">
        <select data-session-server="${attr(item.id)}" aria-label="${attr(item.id)} server" ${running || store.replay ? "disabled" : ""}>${profileOptions}</select><span class="path" title="${attr(item.workspace)}">${html(item.workspace)}</span>
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
    ${head("remember")}${head("recall")}`;
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
    ${approvalChoices(cfg.approval?.mode)}
    <p class="settings-note">Shell always requires confirmation while the service identity is enabled.</p>
    ${number("run.queue_depth", "queue depth", cfg.run?.queue_depth)}`;
}

function shell(active) {
	const service = store.config.shell?.service_account || {};
	const credential = store.shell_credential || {};
	const stored = credential.stored
		? `stored ${credential.stored_at || "(time unavailable)"}`
		: "not stored";
	const accountState = !serviceAccountStatus.loaded
		? "checking local account…"
		: !serviceAccountStatus.supported
			? "local account setup is available only on Windows"
			: serviceAccountStatus.exists
				? `${service.account || "agentb-svc"} · ${serviceAccountStatus.enabled ? "enabled" : "disabled"}${serviceAccountStatus.administrator ? " · ADMINISTRATOR — refused" : !serviceAccountStatus.users_member ? " · Users membership missing" : " · non-admin"}`
				: "not created";
	const setupAction = serviceAccountStatus.exists ? "reset" : "create";
	const setupLabel = serviceAccountBusy
		? "Waiting for Windows UAC…"
		: serviceAccountStatus.exists
			? "Reset password"
			: "Create account";
	const setupDisabled = serviceAccountBusy || !serviceAccountStatus.loaded || !serviceAccountStatus.supported || serviceAccountStatus.administrator;
	const profile = store.servers.find((item) => item.id === selectedHardeningServerID());
	const protectionReady = hardeningStatus.acl?.applied && hardeningStatus.firewall?.applied;
	const elevationState = !hardeningStatus.loaded
		? "checking process elevation…"
		: hardeningStatus.harness_elevated
			? "already elevated · Windows will not show UAC"
			: "standard user token · Windows may request UAC";
	const protectionState = !hardeningStatus.loaded
		? "checking host protections…"
		: !hardeningStatus.supported
			? "available only on Windows"
			: protectionReady
				? "ACL + outbound policy verified"
				: `${hardeningStatus.acl?.summary || "ACL not applied"} · ${hardeningStatus.firewall?.summary || "firewall not applied"}`;
	const applyBlocker = drafts.size
		? "Save pending settings before applying host protections."
		: serviceAccountBusy
			? "Wait for the service-account operation to finish."
			: !service.enabled
				? "Enable the service identity and save first."
				: !credential.stored
					? "Store the service-account credential first."
					: !serviceAccountStatus.exists
						? "Create the service account first."
						: serviceAccountStatus.administrator
							? "The service account is an Administrator and cannot be used."
							: !profile
								? "Test and select a runnable model connection first."
								: "";
	const canApply = !hardeningBusy && !applyBlocker;
	const canInspect = !hardeningBusy && hardeningStatus.loaded && hardeningStatus.supported && serviceAccountStatus.exists;
	const canTestIdentity = !serviceAccountBusy && credential.stored && protectionReady;
	const protectionFeedback = applyBlocker
		? `Apply unavailable: ${applyBlocker}${hardeningMessage ? ` Last result: ${hardeningMessage}` : ""}`
		: hardeningMessage;
  return `<div class="settings-subhead">Service identity</div>
	<p class="settings-note">Use the operator control beside Stop to run tools temporarily as your Windows account.</p>
    ${row("status", `<span class="account-status"><span class="lamp ${serviceAccountStatus.administrator ? "alarm" : serviceAccountStatus.exists ? "live" : ""}"></span>${html(accountState)}</span>`)}
    ${row("credential", `<span class="account-status">${html(stored)}</span>`)}
    ${row("new password", `<input id="service-account-setup-password" type="password" autocomplete="new-password" aria-label="New service-account password" ${setupDisabled ? "disabled" : ""}>`)}
    ${row("repeat", `<input id="service-account-setup-confirmation" type="password" autocomplete="new-password" aria-label="Repeat new service-account password" ${setupDisabled ? "disabled" : ""}>`)}
    <div class="settings-actions">
      <button type="button" data-action="setup-service-account" data-setup-action="${setupAction}" ${setupDisabled ? "disabled" : ""}>${setupLabel}</button>
      <button type="button" data-action="test-shell-credential" title="${protectionReady ? "" : "Apply host protection before testing workspace access."}" ${canTestIdentity ? "" : "disabled"}>Test identity</button>
      <button type="button" data-action="refresh-service-account" ${serviceAccountBusy ? "disabled" : ""}>Refresh</button>
    </div>
    ${feedback(serviceAccountMessage, serviceAccountAlarm, "The non-admin Windows account used by shell and file tools. Windows may request approval.")}
	<div class="settings-subhead">Host protections</div>
	${row("Agent_b", `<span class="account-status ${hardeningStatus.harness_elevated ? "alarm" : ""}">${html(elevationState)}</span>`)}
	${row("status", `<span class="account-status"><span class="lamp ${protectionReady ? "live" : hardeningStatus.loaded ? "alarm" : ""}"></span>${html(protectionState)}</span>`)}
	${row("model route", `<select id="hardening-server" aria-label="Model route for host protections">${hardeningProfiles()}</select>`)}
	<div class="settings-actions">
	  <button type="button" data-action="apply-hardening" title="${attr(applyBlocker)}" aria-busy="${hardeningBusy}" ${canApply ? "" : "disabled"}>${hardeningBusy ? "Working…" : drafts.size ? "Save first" : "Apply protection"}</button>
	  <button type="button" data-action="verify-hardening" ${canInspect ? "" : "disabled"}>Verify</button>
	  <button type="button" data-action="refresh-hardening">Refresh</button>
	  <button type="button" class="${armed.has("hardening:remove") ? "confirm" : ""}" data-action="remove-hardening" ${canInspect ? "" : "disabled"}>${armed.has("hardening:remove") ? "Confirm remove" : "Remove"}</button>
	</div>
	${feedback(protectionFeedback, hardeningAlarm || !!applyBlocker, "Apply protection requests Windows approval, grants workspace access, then tests the service identity.")}
    <details class="settings-advanced">
      <summary>Advanced</summary>
      ${toggle("shell.service_account.enabled", "service identity", service.enabled)}
      ${text("shell.command", "shell command", (store.config.shell?.command || []).join(" "), "command")}
      ${text("shell.service_account.account", "account", service.account || "agentb-svc")}
      ${text("shell.service_account.domain", "domain", service.domain || ".")}
      ${row("store credential", '<input id="shell-service-password" type="password" autocomplete="new-password" aria-label="Service-account credential">')}
      <div class="settings-actions">
        <button type="button" data-action="store-shell-credential" ${serviceAccountBusy ? "disabled" : ""}>Store credential</button>
        <button type="button" data-action="clear-shell-credential" ${serviceAccountBusy ? "disabled" : ""}>Clear credential</button>
      </div>
      ${feedback(shellCredentialMessage, shellCredentialAlarm, "Credentials are encrypted for this Windows user and never returned to the browser.")}
    </details>`;
}

function feedback(message, alarm, fallback) {
	const value = message || fallback || "";
	return `<p class="settings-feedback ${alarm ? "alarm" : ""}" role="status" title="${attr(value)}">${html(value)}</p>`;
}

function hardeningProfiles() {
	const selected = selectedHardeningServerID();
	const options = store.servers
		.filter((profile) => !profileReason(profile))
		.map((profile) => `<option value="${attr(profile.id)}" ${profile.id === selected ? "selected" : ""}>${html(profile.label)} · ${html(profile.base_url)}</option>`)
		.join("");
	return options || '<option value="">No ready connection</option>';
}

function sessionControls(active) {
  if (!active) return '<p class="settings-note">No active session.</p>';
  const resetKey = `reset:${active.id}`;
  return `<div class="settings-actions vertical">
      <button type="button" class="${armed.has(resetKey) ? "confirm" : ""}" data-action="reset-session" data-id="${attr(active.id)}">${armed.has(resetKey) ? "Confirm reset" : "Reset session"}</button>
    </div>
    ${copyRow("JSONL", active.log_path || "")}`;
}

function row(label, control, extra = "") {
  return `<div class="setting-row ${extra}"><label>${html(label)}</label><div>${control}</div></div>`;
}

function current(path, fallback) {
  return drafts.has(path) ? drafts.get(path) : fallback ?? "";
}

function currentValue(path, fallback) {
  if (!drafts.has(path)) return fallback;
  return draftValue(drafts.get(path), draftKinds.get(path));
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
  const selected = !!currentValue(path, value);
  return field(path, label, `<button type="button" role="switch" aria-checked="${selected}" class="switch ${selected ? "on" : ""}" data-action="config-toggle" data-path="${attr(path)}" data-value="${selected ? "false" : "true"}"></button>`);
}

function choices(path, label, values, selected) {
  selected = currentValue(path, selected);
  return field(path, label, `<span class="choice-row">${values.map((value) => `<button type="button" class="${value === selected ? "selected" : ""}" data-action="config-choice" data-path="${attr(path)}" data-value="${attr(value)}">${html(value)}</button>`).join("")}</span>`);
}

function approvalChoices(selected) {
  selected = currentValue("approval.mode", selected);
  const displayed = selected === "off" ? "boundary-only" : selected;
  const modes = [
    ["boundary-only", "Tools run without generic confirmation; Windows still gates anything outside your permissions."],
    ["mutating", "Confirm every file write, edit, and shell command."],
    ["all", "Confirm every tool call."],
  ];
  return field("approval.mode", "approval mode", `<span class="approval-choices">${modes.map(([value, explanation]) => `<button type="button" class="${value === displayed ? "selected" : ""}" data-action="config-choice" data-path="approval.mode" data-value="${attr(value)}"><strong>${html(value)}</strong><span>${html(explanation)}</span></button>`).join("")}</span>`);
}

function copyRow(label, value) {
  return row(label, `<span class="copy-value"><code title="${attr(value)}">${html(value || "—")}</code><button type="button" data-action="copy" data-value="${attr(value)}">copy</button></span>`);
}

async function click(event) {
  const button = event.target.closest("[data-action]");
  if (!button) return;
  const action = button.dataset.action;
  const id = button.dataset.id;
  if (action === "close") return closeSettings();
  if (action === "settings-section") {
    activeSection = id;
    history.replaceState(null, "", `#settings/${activeSection}`);
    return render();
  }
  if (action === "profile-toggle") {
    expanded.has(id) ? expanded.delete(id) : expanded.add(id);
    return render();
  }
  if (action === "show-key") {
    shownKeys.has(id) ? shownKeys.delete(id) : shownKeys.add(id);
    return render();
  }
  if (action === "save-settings") return saveSettings();
  if (action === "config-toggle" || action === "config-choice") {
    const path = button.dataset.path;
    const value = action === "config-toggle" ? button.dataset.value === "true" : button.dataset.value;
    drafts.set(path, value);
    draftKinds.set(path, action === "config-toggle" ? "boolean" : "text");
    settingsSaveMessage = "Unsaved changes";
    settingsSaveAlarm = false;
    return render();
  }
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
	if (action === "setup-service-account") return setupServiceAccount(button.dataset.setupAction);
	if (action === "refresh-service-account") return refreshServiceAccountStatus();
	if (action === "apply-hardening") return hardeningAction("apply");
	if (action === "verify-hardening") return hardeningAction("verify");
	if (action === "refresh-hardening") return refreshHardeningStatus();
	if (action === "remove-hardening") {
		if (!armed.has("hardening:remove")) {
			armed.add("hardening:remove");
			return render();
		}
		armed.delete("hardening:remove");
		return hardeningAction("remove");
	}
  if (action === "copy") {
    if (button.dataset.value) await navigator.clipboard?.writeText(button.dataset.value);
  }
}

async function refreshServiceAccountStatus(preserveMessage = false) {
	try {
		const status = await api("/api/service-account", undefined, "GET");
		serviceAccountStatus = { ...status, loaded: true };
		if (!preserveMessage) {
			serviceAccountMessage = "";
			serviceAccountAlarm = false;
		}
	} catch (error) {
		serviceAccountStatus = { loaded: true, supported: false, exists: false, administrator: false };
		serviceAccountMessage = error.message;
		serviceAccountAlarm = true;
	}
	if (open) render();
}

function selectedHardeningServerID() {
	const ready = store.servers.filter((profile) => !profileReason(profile));
	if (ready.some((profile) => profile.id === hardeningServerID)) return hardeningServerID;
	const activeID = store.sessions[store.active]?.server_id;
	hardeningServerID = ready.some((profile) => profile.id === activeID) ? activeID : ready[0]?.id || "";
	return hardeningServerID;
}

async function refreshHardeningStatus(preserveMessage = false) {
	const serverID = selectedHardeningServerID();
	if (!serverID) {
		hardeningStatus = { loaded: true, supported: true, applied: false };
		hardeningMessage = "select a model profile before applying host protections";
		hardeningAlarm = true;
		if (open) render();
		return;
	}
	try {
		const status = await api(`/api/hardening?server_id=${encodeURIComponent(serverID)}`, undefined, "GET");
		hardeningStatus = { ...status, loaded: true };
		const operation = status.operation || {};
		hardeningBusy = operation.state === "running";
		if (operation.message && (!preserveMessage || operation.state === "running")) {
			hardeningMessage = operation.message;
			hardeningAlarm = operation.state === "failed";
		} else if (!preserveMessage) {
			hardeningMessage = "";
			hardeningAlarm = false;
		}
	} catch (error) {
		hardeningStatus = { loaded: true, supported: true, applied: false };
		hardeningMessage = error.message;
		hardeningAlarm = true;
	}
	if (open) render();
}

async function hardeningAction(action) {
	const serverID = selectedHardeningServerID();
	if (!serverID) {
		hardeningMessage = "Test and select a runnable model connection before changing host protections.";
		hardeningAlarm = true;
		return render();
	}
	hardeningBusy = true;
	hardeningAlarm = false;
	hardeningMessage = action === "verify"
		? "Verifying ACL and outbound policy…"
		: hardeningStatus.harness_elevated
			? "Agent_b is already elevated; applying directly without a UAC prompt."
			: "Windows elevation requested. Respond if a UAC prompt appears.";
	render();
	try {
		const result = await api("/api/hardening", { action, server_id: serverID });
		hardeningStatus = { ...(result.status || hardeningStatus), loaded: true };
		if (result.operation) hardeningStatus.operation = result.operation;
		hardeningMessage = result.operation?.message || result.message;
		hardeningAlarm = result.ok === false || result.operation?.state === "failed" || (action === "apply" && !hardeningStatus.applied);
	} catch (error) {
		hardeningMessage = error.message;
		hardeningAlarm = true;
		await refreshHardeningStatus(true);
	} finally {
		hardeningBusy = hardeningStatus.operation?.state === "running";
		if (open) render();
	}
}

async function setupServiceAccount(action) {
	const passwordInput = sheet.querySelector("#service-account-setup-password");
	const confirmationInput = sheet.querySelector("#service-account-setup-confirmation");
	const password = passwordInput?.value || "";
	const confirmation = confirmationInput?.value || "";
	if (passwordInput) passwordInput.value = "";
	if (confirmationInput) confirmationInput.value = "";
	if (!password || password.length < 14 || password !== confirmation) {
		serviceAccountMessage = !password
			? "password is required"
			: password.length < 14
				? "password must contain at least 14 characters"
				: "the two password entries do not match";
		serviceAccountAlarm = true;
		return render();
	}
	serviceAccountBusy = true;
	serviceAccountAlarm = false;
	serviceAccountMessage = "Approve the Windows UAC prompt to run the account setup script.";
	render();
	try {
		const result = await api("/api/service-account", { action, password, confirmation });
		serviceAccountStatus = { ...(result.account || serviceAccountStatus), loaded: true };
		store.shell_credential = result.credential || store.shell_credential;
		store.shell_identity = result.identity || store.shell_identity;
		if (result.config) reduce({ type: "config.changed", data: { config: result.config } });
		serviceAccountMessage = result.message;
		serviceAccountAlarm = !result.ok;
		await refreshHardeningStatus();
	} catch (error) {
		if (error.data?.credential) store.shell_credential = error.data.credential;
		serviceAccountMessage = error.message;
		serviceAccountAlarm = true;
		await refreshServiceAccountStatus(true);
	} finally {
		serviceAccountBusy = false;
		if (open) render();
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
		shellCredentialAlarm = false;
	} catch (error) {
		shellCredentialMessage = error.message;
		shellCredentialAlarm = true;
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
			serviceAccountMessage = result.message;
			serviceAccountAlarm = false;
		}
		shellCredentialAlarm = false;
	} catch (error) {
		if (action === "test") {
			serviceAccountMessage = error.message;
			serviceAccountAlarm = true;
		} else {
			shellCredentialMessage = error.message;
			shellCredentialAlarm = true;
		}
	}
	render();
}

async function blur(event) {
  const input = event.target;
  if (input.matches(".setting-input[data-path]")) {
    const path = input.dataset.path;
    if (input.dataset.kind === "secret" && input.value === "•••• set") return;
    drafts.set(path, input.value);
    draftKinds.set(path, input.dataset.kind || "text");
    settingsSaveMessage = "Unsaved changes";
    settingsSaveAlarm = false;
    refreshSaveControls();
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

async function change(event) {
  if (event.target.matches("#hardening-server")) {
    hardeningServerID = event.target.value;
    hardeningStatus = { loaded: false, supported: true, applied: false };
    hardeningMessage = "";
    render();
    await refreshHardeningStatus();
    return;
  }
  const select = event.target.closest("[data-session-server]");
  if (!select) return;
  const id = select.dataset.sessionServer;
  try {
    const result = await api(`/api/sessions/${encodeURIComponent(id)}`, { server_id: select.value });
    errors.delete(`session.${id}`);
    if (result.session) {
      reduce({ type: "session.updated", session_id: id, data: result.session });
      setActive(id);
    }
  } catch (error) {
    errors.set(`session.${id}`, error.message);
    render();
  }
}

function draftValue(raw, kind = "text") {
  if (kind === "boolean") return raw === true || raw === "true";
  if (kind === "number") return Number(raw);
  if (kind === "percent") return Number(raw) / 100;
  if (kind === "list") return String(raw).split(",").map((value) => value.trim()).filter(Boolean);
  if (kind === "command") return String(raw).trim().split(/\s+/).filter(Boolean);
  return raw;
}

function combinedPatch(entries) {
  const result = {};
  const servers = new Map();
  for (const [path, raw] of entries) {
    const parts = path.split(".");
    const value = draftValue(raw, draftKinds.get(path));
    if (parts[0] === "servers") {
      const item = servers.get(parts[1]) || { id: parts[1] };
      assign(item, parts.slice(2), value);
      servers.set(parts[1], item);
    } else assign(result, parts, value);
  }
  if (servers.size) result.servers = [...servers.values()];
  return result;
}

async function saveSettings() {
  if (settingsSaving || !drafts.size) return;
  const entries = [...drafts.entries()];
  const changedPaths = entries.map(([path]) => path);
  settingsSaving = true;
  settingsSaveMessage = "Saving changes…";
  settingsSaveAlarm = false;
  refreshSaveControls();
  try {
    const result = await api("/api/config", combinedPatch(entries));
    for (const path of changedPaths) {
      drafts.delete(path);
      draftKinds.delete(path);
      errors.delete(path);
    }
    reduce({ type: "config.changed", data: { config: result } });
    settingsSaveMessage = "All changes saved";
    if (changedPaths.some((path) => path === "shell.service_account.account" || path === "shell.service_account.domain"))
      await refreshServiceAccountStatus();
  } catch (error) {
    errors.set(error.field || "config", error.message);
    settingsSaveMessage = `Save failed: ${error.message}`;
    settingsSaveAlarm = true;
  } finally {
    settingsSaving = false;
    if (open) render();
  }
}

function assign(target, parts, value) {
  let node = target;
  parts.forEach((part, index) => {
    if (index === parts.length - 1) node[part] = value;
    else node = node[part] ||= {};
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
