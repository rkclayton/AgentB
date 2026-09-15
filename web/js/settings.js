import { api, reduce, setActive, store, subscribe } from "./bus.js";
import { operatorStatusView } from "./operator-status.js";
import { navigationSurfaceReady } from "./navigation-telemetry.js";
import { renderConnectionsPage } from "./settings-connections.js";
import { renderAboutPage } from "./settings-about.js";
import { renderContextPage } from "./settings-context.js";
import { renderDeliveryPage } from "./settings-delivery.js";
import { renderGeneralPage } from "./settings-general.js";
import { renderNotificationsPage } from "./settings-notifications.js";
import { renderRunPage } from "./settings-run.js";
import { renderSecurityPage } from "./settings-security.js";
import { renderWorkspacePage } from "./settings-workspace.js";

const sheet = document.getElementById("settings-page");
let gear;
const expanded = new Set();
const armed = new Set();
const drafts = new Map();
const draftKinds = new Map();
const errors = new Map();
const probeMessages = new Map();
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
let signingStatus = { loaded: false, supported: true, configured: false, can_manage: false, files: [] };
let signingBusy = false;
let signingMessage = "";
let signingAlarm = false;
let notificationStatus = { configured: false, host: "" };
let notificationBusy = false;
let notificationMessage = "";
let notificationAlarm = false;
let settingsSaving = false;
let settingsSaveMessage = "All changes saved";
let settingsSaveAlarm = false;
let activeSection = "servers";
let hardeningServerID = "";
let workspaceState = [];
let operatorFileState = { attachment_files: 0, attachment_bytes: 0, instruction_found: [] };
const serverProfiles = () => Array.isArray(store.servers) ? store.servers : [];

const sectionLabels = [
  ["servers", "Connections"],
  ["context", "Context"],
  ["run", "Run & approval"],
  ["delivery", "Delivery"],
  ["notifications", "Notifications"],
  ["shell", "Security"],
  ["about", "About"],
];

export function initSettings() {
  gear = document.querySelector(".shell-settings");
  gear.addEventListener("click", (event) => {
    event.preventDefault();
    open ? closeSettings() : openSettings();
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
	if (event.type === "notification.changed") notificationStatus = event.data || notificationStatus;
    if (event.type === "server.probed") {
      const profileID = event.data?.server_id || "";
      const findings = event.data?.capabilities?.findings || event.data?.findings || [];
      const failed = findings.find((value) => String(value).startsWith("probe failed:"));
      probeMessages.set(profileID, { message: failed ? `Test failed — ${String(failed).slice(13).trim()}` : "Test passed", alarm: !!failed });
    }
    if (
      open && (
      [
        "init",
        "snapshot",
        "active.changed",
        "config.changed",
		"notification.changed",
		"shell.identity",
		"shell.credential",
        "server.probed",
      ].includes(event.type) || (event.type === "projection.patch" && (event.data?.operations || []).some((operation) =>
        ["/label", "/agent_id", "/server_id", "/agent_name", "/b_profile", "/role", "/plan_id", "/plan_name", "/runnable", "/not_runnable_reason", "/tools", "/memory_path", "/memory_content", "/agent_memory_path", "/agent_memory_content", "/budget", "/closed"].includes(operation.path))))
    )
      render();
    if (open && event.type === "snapshot") {
      refreshServiceAccountStatus();
      refreshHardeningStatus();
	  refreshSigningStatus();
	  refreshNotificationStatus();
	  refreshOperatorFileState();
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
  history.replaceState(null, "", `#settings/${activeSection}`);
  render();
  refreshServiceAccountStatus();
	refreshHardeningStatus();
	refreshSigningStatus();
	refreshNotificationStatus();
  refreshWorkspaceState();
  refreshOperatorFileState();
  requestAnimationFrame(() => sheet.querySelector(".settings-nav button.selected")?.focus());
}

export function closeSettings(surface = "console") {
  if (!open) return;
  open = false;
  sheet.hidden = true;
  sheet.setAttribute("aria-hidden", "true");
  gear.setAttribute("aria-expanded", "false");
  gear.setAttribute("aria-label", "Settings");
  gear.setAttribute("aria-pressed", "false");
  gear.classList.remove("selected");
  history.replaceState(null, "", `${location.pathname}${location.search}`);
  (lastFocus || gear).focus();
  navigationSurfaceReady(surface, store);
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
    servers: () => renderConnectionsPage(settingsPageContext(active)),
    sessions: () => renderGeneralPage("sessions", active, settingsPageContext(active)),
    tools: () => renderGeneralPage("tools", active, settingsPageContext(active)),
    memory: () => renderGeneralPage("memory", active, settingsPageContext(active)),
    context: () => renderContextPage(active, settingsPageContext(active)),
    run: () => renderRunPage(settingsPageContext(active)),
    delivery: () => renderDeliveryPage(settingsPageContext(active)),
    notifications: () => renderNotificationsPage(settingsPageContext(active)),
    shell: () => renderSecurityPage("shell", active, settingsPageContext(active)) + renderWorkspacePage(settingsPageContext(active)),
    about: () => renderAboutPage(settingsPageContext(active)),
    session: () => renderSecurityPage("session", active, settingsPageContext(active)),
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
  navigationSurfaceReady("settings", store);
}

function settingsPageContext(active) {
  return {
    active, store, expanded, armed, drafts, errors, probeMessages, workspaceState, operatorFileState,
    shellCredentialMessage, shellCredentialAlarm, serviceAccountStatus, serviceAccountBusy,
    serviceAccountMessage, serviceAccountAlarm, hardeningStatus, hardeningBusy, hardeningMessage,
    hardeningAlarm, signingStatus, signingBusy, signingMessage, signingAlarm, serverProfiles,
    notificationStatus, notificationBusy, notificationMessage, notificationAlarm,
    row, field, text, number, numberControl, textarea, secret, toggle, choices, approvalChoices,
    copyRow, currentValue, issue, profileReason, html, attr, selectedHardeningServerID, operatorStatusView,
  };
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

async function refreshWorkspaceState() {
	try { workspaceState=await api("/api/workspaces",undefined,"GET") } catch { workspaceState=[] }
	if(open&&activeSection==="shell")render();
}

async function refreshOperatorFileState() {
	const dir=store.sessions[store.active]?.workspace||store.config.workspace||"";
	try { operatorFileState=await api(`/api/operator-files?dir=${encodeURIComponent(dir)}`,undefined,"GET") }
	catch { operatorFileState={attachment_files:0,attachment_bytes:0,instruction_found:[]} }
	if(open&&activeSection==="shell")render();
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
  const control = `${numberControl(path, value, step, disabled, kind)}${note ? `<span class="control-note">${html(note)}</span>` : ""}`;
  return field(path, label, control, alarm);
}

function numberControl(path, value, step = "1", disabled = false, kind = "number") {
  return `<input class="setting-input number" type="number" step="${step}" data-path="${attr(path)}" data-kind="${kind}" value="${attr(current(path, value))}" ${disabled ? "disabled" : ""}>`;
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

function selectSetting(path, label, options, selected) {
	selected = currentValue(path, selected);
	return field(path, label, `<select class="setting-input" data-path="${attr(path)}" data-kind="text">${options.map(([value, text]) => `<option value="${attr(value)}" ${value === selected ? "selected" : ""}>${html(text)}</option>`).join("")}</select>`);
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
    const wasOpen = expanded.has(id);
    expanded.clear();
    if (!wasOpen) expanded.add(id);
    return render();
  }
  if (action === "show-key") {
    shownKeys.has(id) ? shownKeys.delete(id) : shownKeys.add(id);
    return render();
  }
  if (action === "save-settings") return saveSettings();
  if (action === "operator-context") {
    try { await api("/api/config", {shell:{operator_context:!store.shell_identity?.operator_context}}); }
    catch (error) { errors.set("shell", error.message); render(); }
    return;
  }
	if (action === "local-network-toggle") {
		const enabled = button.getAttribute("aria-checked") !== "true";
		button.setAttribute("aria-checked", String(enabled));
		button.classList.toggle("on", enabled);
		for (const input of sheet.querySelectorAll("[data-local-subnet]")) input.disabled = !enabled;
		return;
	}
  if (action === "open-setup") { location.href = "/setup?from=settings"; return; }
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
    const pendingPrefix = `servers.${id}.`;
    if ([...drafts.keys()].some((path) => path.startsWith(pendingPrefix))) {
      probeMessages.set(id, { message: "Saving before Test…", alarm: false });
      render();
      if (!await saveSettings(pendingPrefix)) {
        probeMessages.set(id, { message: "Test not run — save failed", alarm: true });
        return render();
      }
    }
    const profile = serverProfiles().find((x) => x.id === id);
    if (profile) profile._probing = true;
    probeMessages.set(id, { message: "Testing…", alarm: false });
    render();
    try {
      await api(`/api/servers/${encodeURIComponent(id)}/probe`);
    } catch (error) {
      if (profile) profile._probing = false;
      errors.set(`servers.${id}`, error.message);
      probeMessages.set(id, { message: `Test failed — ${error.message}`, alarm: true });
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
  if (action === "revoke-workspace-policy") {
		const key=`policy:${id}`; if(!armed.has(key)){armed.add(key);return render()} armed.delete(key);
		try{await api("/api/workspaces/policy-revoke",{dir:id});await refreshWorkspaceState();reduce({type:"snapshot",data:await api("/api/state",undefined,"GET")})}catch(error){errors.set("workspace",error.message);render()} return;
	}
  if (action === "empty-operator-attachments") {
	const key="operator-attachments:empty";
	if(!armed.has(key)){armed.add(key);return render()}
	armed.delete(key);
	try{await api("/api/operator-files",{action:"empty_attachments",confirm:true});await refreshOperatorFileState()}
	catch(error){errors.set("workspace",error.message);render()}
	return;
  }
  if (action === "adopt-instructions") {
	const cleanup=sheet.querySelector("#adopt-instruction-cleanup")?.checked===true;
	try{await api("/api/operator-files",{action:"adopt_instructions",dir:id,cleanup,confirm_cleanup:cleanup});await refreshOperatorFileState();await refreshWorkspaceState()}
	catch(error){errors.set("workspace",error.message);render()}
	return;
  }
  if (action === "session-tool-toggle") {
    const active = store.sessions[store.active];
    if (!active) return;
    try {
      await api(`/api/tools/${encodeURIComponent(id)}`, {
        session_id: active.id,
        enabled: button.dataset.enabled !== "true",
      });
    } catch (error) {
      errors.set(`tools.${id}`, error.message);
      render();
    }
    return;
  }
	if (action === "store-shell-credential") return storeShellCredential();
	if (action === "test-shell-credential") return shellCredentialAction("test");
	if (action === "clear-shell-credential") return shellCredentialAction("clear");
	if (action === "setup-service-account") return setupServiceAccount(button.dataset.setupAction);
	if (action === "refresh-service-account") return refreshServiceAccountStatus();
	if (action === "apply-hardening") return hardeningAction("apply");
	if (action === "verify-hardening") return hardeningAction("verify");
	if (action === "refresh-hardening") return refreshHardeningStatus();
	if (action === "save-notification") return notificationAction("save");
	if (action === "test-notification") return notificationAction("test");
	if (action === "clear-notification") return notificationAction("clear");
	if (action === "remove-hardening") {
		if (!armed.has("hardening:remove")) {
			armed.add("hardening:remove");
			return render();
		}
		armed.delete("hardening:remove");
		return hardeningAction("remove");
	}
	if (action === "create-signing") return signingAction("create");
	if (action === "import-signing") return importSigning();
	if (action === "sign-application") return signingAction("sign");
	if (action === "verify-signing") return refreshSigningStatus();
	if (action === "export-signing") return exportSigning();
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
	const ready = serverProfiles().filter((profile) => !profileReason(profile));
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
		const lanSwitch = sheet.querySelector('[data-action="local-network-toggle"]');
		const allowLocalNetwork = action === "apply" ? lanSwitch?.getAttribute("aria-checked") === "true" : !!store.config.shell?.allow_local_network;
		const localSubnets = action === "apply" && allowLocalNetwork
			? [...sheet.querySelectorAll("[data-local-subnet]:checked")].map((input) => input.value)
			: store.config.shell?.confirmed_local_subnets || [];
		const result = await api("/api/hardening", { action, server_id: serverID, allow_local_network: allowLocalNetwork, local_subnets: localSubnets });
		hardeningStatus = { ...(result.status || hardeningStatus), loaded: true };
		if (action === "apply" && result.ok !== false) {
			store.config.shell.allow_local_network = allowLocalNetwork;
			store.config.shell.confirmed_local_subnets = allowLocalNetwork ? localSubnets : [];
		}
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

async function refreshNotificationStatus() {
	try {
		notificationStatus = await api("/api/notifications", undefined, "GET");
	} catch (error) {
		notificationStatus = { configured: false, host: "" };
		notificationMessage = error.message;
		notificationAlarm = true;
	}
	if (open && activeSection === "notifications") render();
}

async function notificationAction(action) {
	const input = sheet.querySelector("#discord-webhook-url");
	const value = input?.value || "";
	notificationBusy = true;
	notificationAlarm = false;
	notificationMessage = action === "test" ? "Sending test…" : "Saving…";
	if (input) input.value = "";
	render();
	try {
		const result = await api("/api/notifications", action === "save" ? { action, url: value } : { action });
		notificationStatus = result.notifications || notificationStatus;
		notificationMessage = action === "test" ? "Test sent." : action === "clear" ? "Discord notifications disabled." : "Discord webhook saved.";
	} catch (error) {
		notificationMessage = error.message;
		notificationAlarm = true;
	} finally {
		notificationBusy = false;
		if (open) render();
	}
}

async function refreshSigningStatus(preserveMessage = false) {
	try {
		const status = await api("/api/signing", undefined, "GET");
		signingStatus = { ...status, loaded: true };
		if (!preserveMessage) { signingMessage = ""; signingAlarm = false; }
	} catch (error) {
		signingStatus = { loaded: true, supported: false, configured: false, can_manage: false, files: [] };
		signingMessage = error.message;
		signingAlarm = true;
	}
	if (open) render();
}

async function signingAction(action, fields = {}) {
	signingBusy = true;
	signingAlarm = false;
	signingMessage = action === "sign" ? "Approve Windows UAC; Agent_b will restart after verification." : `${action} in progress…`;
	render();
	try {
		const response = await api("/api/signing", { action, ...fields });
		if (response.config) reduce({ type: "config.changed", data: { config: response.config } });
		signingMessage = response.result?.message || `${action} complete`;
		if (action !== "sign") await refreshSigningStatus(true);
	} catch (error) {
		signingMessage = error.message;
		signingAlarm = true;
	} finally {
		signingBusy = false;
		if (open) render();
	}
}

async function importSigning() {
	const file = sheet.querySelector("#signing-pfx")?.files?.[0];
	const password = sheet.querySelector("#signing-password")?.value || "";
	const thumbprint = sheet.querySelector("#signing-thumbprint")?.value || "";
	const passwordInput = sheet.querySelector("#signing-password");
	if (passwordInput) passwordInput.value = "";
	if (!file && thumbprint) return signingAction("select", { thumbprint });
	if (!file || !password) {
		signingMessage = !file ? "Choose a .pfx file or a stored certificate." : "Enter the PFX password.";
		signingAlarm = true;
		return render();
	}
	const bytes = new Uint8Array(await file.arrayBuffer());
	let binary = "";
	for (const value of bytes) binary += String.fromCharCode(value);
	bytes.fill(0);
	return signingAction("import", { pfx_base64: btoa(binary), password });
}

async function exportSigning() {
	try {
		const response = await fetch("/api/signing", {
			method: "POST",
			headers: { "Content-Type": "application/json", "X-AgentB-Mutation-Token": store.mutation_token },
			body: JSON.stringify({ action: "export" }),
		});
		if (!response.ok) throw new Error((await response.json()).error || `HTTP ${response.status}`);
		const link = document.createElement("a");
		link.href = URL.createObjectURL(await response.blob());
		link.download = "Agent_b-code-signing.cer";
		link.click();
		URL.revokeObjectURL(link.href);
		signingMessage = "Public certificate exported.";
		signingAlarm = false;
	} catch (error) { signingMessage = error.message; signingAlarm = true; }
	if (open) render();
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
    await api(`/api/sessions/${encodeURIComponent(id)}`, { server_id: select.value });
    errors.delete(`session.${id}`);
    reduce({ type: "snapshot", data: await api("/api/state", undefined, "GET") });
    setActive(id);
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

async function saveSettings(pathPrefix = "") {
  if (settingsSaving) return false;
  const entries = [...drafts.entries()].filter(([path]) => !pathPrefix || path.startsWith(pathPrefix));
  if (!entries.length) return true;
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
    settingsSaveMessage = drafts.size ? `${drafts.size} unsaved change${drafts.size === 1 ? "" : "s"} remain` : "All changes saved";
    if (changedPaths.some((path) => path === "shell.service_account.account" || path === "shell.service_account.domain"))
      await refreshServiceAccountStatus();
  } catch (error) {
    errors.set(error.field || "config", error.message);
    settingsSaveMessage = `Save failed: ${error.message}`;
    settingsSaveAlarm = true;
    return false;
  } finally {
    settingsSaving = false;
    if (open) render();
  }
  return true;
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
  expanded.clear();
  expanded.add(id);
  try {
    const result = await api("/api/config", { servers: [{ id, label: id, base_url: "http://127.0.0.1:8000", model: "model" }] });
    probeMessages.set(id, { message: "Added and saved — edit, then Test", alarm: false });
    reduce({ type: "config.changed", data: { config: result } });
  } catch (error) {
    expanded.delete(id);
    errors.set("servers", error.message);
    settingsSaveMessage = `Add failed: ${error.message}`;
    settingsSaveAlarm = true;
    render();
  }
}

async function duplicateServer(id) {
  const source = serverProfiles().find((x) => x.id === id);
  if (!source) return;
  const copy = structuredClone(source);
  delete copy._probing;
  copy.id = uniqueID(`${id}-2`);
  copy.label = `${source.label} copy`;
  if (copy.api_key === "•••• set") {
    copy.api_key = "";
    copy.credential = "";
  }
  expanded.clear();
  expanded.add(copy.id);
  const result = await api("/api/config", { servers: [copy] });
  reduce({ type: "config.changed", data: { config: result } });
}

function uniqueID(base) {
  let id = base;
  let suffix = 2;
  while (serverProfiles().some((profile) => profile.id === id)) id = `${base}-${suffix++}`;
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
  };
  try {
    const result = await api("/api/sessions", body);
    reduce({ type: "snapshot", data: await api("/api/state", undefined, "GET") });
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
  const nctx = profile.context?.n_ctx;
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
