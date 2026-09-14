let expanded, armed, drafts, errors, probeMessages, serverProfiles, row, text, number, numberControl, textarea, secret, toggle, choices, profileReason, html, attr;
function useSettingsContext(context) {
  ({ expanded, armed, drafts, errors, probeMessages, serverProfiles, row, text, number, numberControl, textarea, secret, toggle, choices, profileReason, html, attr } = context);
}

function servers() {
  const profiles = serverProfiles();
  const rows = profiles
    .map((profile) => {
      const isOpen = expanded.has(profile.id);
      const hasPendingChanges = [...drafts.keys()].some((path) => path.startsWith(`servers.${profile.id}.`));
      const feedback = probeMessages.get(profile.id);
      const reason = profileReason(profile);
      const failed = (profile.capabilities?.findings || []).some((x) =>
        x.startsWith("probe failed:"),
      );
      const testState = profile._probing
        ? "testing"
        : hasPendingChanges
          ? "unsaved — Test will save first"
          : feedback?.message
            ? feedback.message
            : failed
              ? "failed"
              : !reason && profile.capabilities?.probed_at
                ? "ready"
                : "not tested";
      const ready = !reason && !!profile.capabilities?.probed_at;
      const lamp = failed || feedback?.alarm || (reason && reason !== "context length unknown") ? "alarm" : profile._probing || ready || feedback ? "live" : "";
      const removeKey = `server:${profile.id}`;
      return `<div class="profile-row ${isOpen ? "selected" : ""}">
          <button type="button" class="profile-summary" data-action="profile-toggle" data-id="${attr(profile.id)}">
            <span class="lamp ${lamp}"></span><span>${html(profile.label)}</span><span class="profile-url">${html(profile.base_url)}</span><span class="profile-state">${testState}</span>
          </button>
          <button type="button" data-action="probe" data-id="${attr(profile.id)}" title="${hasPendingChanges ? "Test will save this connection first." : ""}" ${profile._probing ? "disabled" : ""}>${profile._probing ? "Testing…" : "Test"}</button>
          <button type="button" class="profile-remove ${armed.has(removeKey) ? "confirm" : ""}" data-action="remove-server" data-id="${attr(profile.id)}" aria-label="${armed.has(removeKey) ? `Confirm remove ${attr(profile.label)}` : `Remove ${attr(profile.label)}`}">${armed.has(removeKey) ? "Confirm ×" : "×"}</button>
      </div>`;
    })
    .join("");
  const editors = profiles.filter((profile) => expanded.has(profile.id)).map((profile) => `<section class="profile-editor" aria-label="${attr(profile.label)} connection settings">
    <div class="profile-editor-head"><div><span class="lamp ${profileReason(profile) && profileReason(profile) !== "context length unknown" ? "alarm" : ""}"></span><h3>${html(profile.label)}</h3><span class="profile-url">${html(profile.base_url)}</span></div><button type="button" data-action="duplicate-server" data-id="${attr(profile.id)}">Duplicate</button></div>
    <div class="profile-fields">${profileFields(profile, profileReason(profile))}</div>
  </section>`).join("");
  return `<div class="settings-actions settings-connections-actions"><button type="button" data-action="open-setup">Open setup guide</button><button type="button" data-action="add-server">Add connection</button></div><div class="settings-subhead">Profiles</div><div class="profile-list">${rows || '<p class="settings-note inline">No connections configured.</p>'}</div>${editors}`;
}

function profileFields(profile, reason) {
  const id = profile.id;
  const p = `servers.${id}`;
  const caps = profile.capabilities || {};
  const efforts = profile.reasoning?.valid_efforts || caps.valid_efforts || [];
  const llama = caps.server === "llama.cpp";
  const findings = (caps.findings || [])
    .map((value) => `<li>${html(value)}</li>`)
    .join("");
  const samplingRows = [
    ["temperature", "temperature", "0.01", false],
    ["top_p", "top_p", "0.01", false],
    ["top_k", "top_k", "1", !llama],
    ["min_p", "min_p", "0.01", !llama],
    ["presence_penalty", "presence penalty", "0.1", false],
    ["repeat_penalty", "repeat penalty", "0.1", !llama],
  ].map(([name, label, step, disabled]) => `<div class="sampling-label">${html(label)}</div>${["thinking", "nonthinking"].map((mode) => `<div>${numberControl(`${p}.sampling.${mode}.${name}`, profile.sampling[mode][name], step, disabled)}${disabled ? '<span class="control-note">llama.cpp only</span>' : ""}</div>`).join("")}`).join("");
	return `<div class="profile-fieldset profile-identity"><h4>Connection</h4>${text(`${p}.label`, "label", profile.label)}
    ${text(`${p}.base_url`, "base_url", profile.base_url)}
	${text(`${p}.extract_url`, "extract_url", profile.extract_url || "")}
	${choices(`${p}.attachment_handling`, "attachment handling", ["auto", "native", "extract"], profile.attachment_handling || "auto")}
	<p class="settings-note">auto follows probed capability; native always sends supported attachment kinds; extract keeps their binary local</p>
    ${text(`${p}.model`, "model", profile.model)}
	${text(`${p}.credential`, "credential ref", profile.credential || "")}
    ${secret(`${p}.api_key`, "api_key", profile.api_key, id)}
	<p class="settings-note">API keys are stored in user-scoped DPAPI storage; configuration keeps only the credential reference.</p>
    ${number(`${p}.request_timeout_s`, "timeout", profile.request_timeout_s)}
    ${choices(`${p}.probe_mode`, "probe mode", ["full", "minimal", "off"], profile.probe_mode)}
    <p class="settings-note">minimal and off skip checks that spend tokens; assumed values are marked in findings</p></div>
    <div class="profile-fieldset profile-reasoning"><h4>Reasoning &amp; context</h4>
    ${choices(`${p}.reasoning.control`, "control", ["auto", "chat_template_kwargs", "top_level", "server_flag", "none"], profile.reasoning.control)}
    ${toggle(`${p}.reasoning.enabled`, "enabled", profile.reasoning.enabled)}
    ${efforts.length ? choices(`${p}.reasoning.effort`, "effort", efforts, profile.reasoning.effort) : row("effort", '<span class="settings-note inline">not supported by this server</span>')}
    ${toggle(`${p}.reasoning.preserve`, "preserve", profile.reasoning.preserve)}
    ${number(`${p}.reasoning.max_tokens`, "reasoning cap", profile.reasoning.max_tokens || 0, "1")}
    ${number(`${p}.context.reserve_output`, "reserve", profile.context.reserve_output)}
	${number(`${p}.context.n_ctx`, "context size", profile.context.n_ctx, "1")}
    <p class="settings-note">Test fills this from the server when available. Otherwise enter the server's configured context window; it is required for use and for probe mode off.</p></div>
    <div class="profile-fieldset profile-sampling"><h4>Sampling</h4><div class="sampling-grid"><div></div><div class="sampling-column">Thinking</div><div class="sampling-column">Non-thinking</div>${samplingRows}</div></div>
    <div class="profile-fieldset profile-prompt"><h4>System prompt</h4>
    ${textarea(`${p}.system_prompt_override`, "system prompt override", profile.system_prompt_override || "")}
    <p class="settings-note">variables: {{folder}} {{plans}} {{tools}} {{agent}} {{project}} {{memory}}</p></div>
    <div class="profile-fieldset profile-capabilities"><h4>Capabilities</h4>
    <div class="findings"><span class="settings-note">${html(caps.probed_at || "not probed")}</span><ul>${findings || "<li>no findings</li>"}</ul></div>
    ${reason && reason !== "context length unknown" ? `<p class="field-error">${html(reason)}</p>` : ""}
    ${errors.get(p) ? `<p class="field-error">${html(errors.get(p))}</p>` : ""}</div>`;
}


export function renderConnectionsPage(context) {
  useSettingsContext(context);
  return servers();
}
