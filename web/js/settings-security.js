let store, armed, drafts, shellCredentialMessage, shellCredentialAlarm, serviceAccountStatus, serviceAccountBusy, serviceAccountMessage, serviceAccountAlarm, hardeningStatus, hardeningBusy, hardeningMessage, hardeningAlarm, signingStatus, signingBusy, signingMessage, signingAlarm, serverProfiles, row, text, toggle, copyRow, profileReason, html, attr, selectedHardeningServerID, operatorStatusView;
function useSettingsContext(context) {
  ({ store, armed, drafts, shellCredentialMessage, shellCredentialAlarm, serviceAccountStatus, serviceAccountBusy, serviceAccountMessage, serviceAccountAlarm, hardeningStatus, hardeningBusy, hardeningMessage, hardeningAlarm, signingStatus, signingBusy, signingMessage, signingAlarm, serverProfiles, row, text, toggle, copyRow, profileReason, html, attr, selectedHardeningServerID, operatorStatusView } = context);
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
	const profile = serverProfiles().find((item) => item.id === selectedHardeningServerID());
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
	const signedFiles = signingStatus.files || [];
	const signaturesValid = signedFiles.length > 0 && signedFiles.every((file) => file.status === "Valid" && file.thumbprint === signingStatus.thumbprint && file.timestamped);
	const certificateDone = signingStatus.configured && signingStatus.has_private_key && signingStatus.code_signing_eku;
	const verifyDone = certificateDone && signingStatus.chain_valid && signaturesValid;
	const signingAllowed = signingStatus.can_manage && !signingBusy;
	const operatorView = operatorStatusView(store.shell_identity);
	const lanEnabled = !!store.config.shell?.allow_local_network;
	const sandboxEnabled = store.config.sandbox?.enabled !== false;
	const sandboxStatus = store.sandbox || {};
	const sandboxState = sandboxStatus.available ? "ready" : `inert · ${sandboxStatus.reason || "Docker Sandbox is unavailable"}`;
	const confirmedSubnets = new Set(store.config.shell?.confirmed_local_subnets || []);
	const detectedSubnets = hardeningStatus.detected_local_subnets || [];
	const subnetChoices = detectedSubnets.length
		? detectedSubnets.map((subnet) => `<label class="settings-note"><input type="checkbox" data-local-subnet value="${attr(subnet)}" ${confirmedSubnets.has(subnet) ? "checked" : ""} ${lanEnabled ? "" : "disabled"}> ${html(subnet)}</label>`).join("")
		: '<p class="settings-note">No private IPv4 LAN subnet detected.</p>';
  return `<div class="settings-subhead">Operator mode</div>
	${row("identity", `<button type="button" class="settings-operator-status" data-action="operator-context" aria-pressed="${operatorView.active}" aria-label="${attr(operatorView.label)}"><img src="${operatorView.src}" srcset="${operatorView.srcset}" width="24" height="24" alt=""><span>${operatorView.active ? "Stop running everything as me" : "Run everything as me for 20 minutes"}</span></button>`)}
	<p class="settings-note">This defeats the service-account OS boundary for every tool in every chat until it expires.</p>
	<div class="settings-subhead">Docker Sandbox</div>
	${toggle("sandbox.enabled", "Docker Sandbox", sandboxEnabled)}
	${row("status", `<span class="account-status"><span class="lamp ${sandboxStatus.available ? "live" : ""}"></span>${html(sandboxState)}</span>`)}
	<p class="settings-note">This install-wide setting routes shell and bash through Docker Sandbox. When Docker Sandbox is unavailable, the setting stays on but is inert and reports why.</p>
	<div class="settings-subhead">Service identity</div>
    ${row("status", `<span class="account-status"><span class="lamp ${serviceAccountStatus.administrator ? "alarm" : serviceAccountStatus.exists ? "live" : ""}"></span>${html(accountState)}</span>`)}
    ${row("credential", `<span class="account-status">${html(stored)}</span>`)}
    ${row("new password", `<input id="service-account-setup-password" type="password" autocomplete="new-password" aria-label="New service-account password" ${setupDisabled ? "disabled" : ""}>`)}
    ${row("repeat", `<input id="service-account-setup-confirmation" type="password" autocomplete="new-password" aria-label="Repeat new service-account password" ${setupDisabled ? "disabled" : ""}>`)}
    <div class="settings-actions">
      <button type="button" data-action="setup-service-account" data-setup-action="${setupAction}" ${setupDisabled ? "disabled" : ""}>${setupLabel}</button>
      <button type="button" data-action="test-shell-credential" title="${protectionReady ? "" : "Apply host protection before testing folder access."}" ${canTestIdentity ? "" : "disabled"}>Test identity</button>
      <button type="button" data-action="refresh-service-account" ${serviceAccountBusy ? "disabled" : ""}>Refresh</button>
    </div>
    ${feedback(serviceAccountMessage, serviceAccountAlarm, "The non-admin Windows account used by shell and file tools. Windows may request approval.")}
	<div class="settings-subhead">Host protections</div>
	${row("Agent_b", `<span class="account-status ${hardeningStatus.harness_elevated ? "alarm" : ""}">${html(elevationState)}</span>`)}
	${row("status", `<span class="account-status"><span class="lamp ${protectionReady ? "live" : hardeningStatus.loaded ? "alarm" : ""}"></span>${html(protectionState)}</span>`)}
	${row("model route", `<select id="hardening-server" aria-label="Model route for host protections">${hardeningProfiles()}</select>`)}
	${row("Allow my local network", `<button type="button" role="switch" aria-checked="${lanEnabled}" class="switch ${lanEnabled ? "on" : ""}" data-action="local-network-toggle"></button>`)}
	<div class="settings-subnets" data-local-subnets>${subnetChoices}</div>
	<p class="settings-note">Select each detected subnet you intend to expose, then Apply protection. Link-local, cloud metadata, and Agent_b's own listener remain refused.</p>
	<div class="settings-actions">
	  <button type="button" data-action="apply-hardening" title="${attr(applyBlocker)}" aria-busy="${hardeningBusy}" ${canApply ? "" : "disabled"}>${hardeningBusy ? "Working…" : drafts.size ? "Save first" : "Apply protection"}</button>
	  <button type="button" data-action="verify-hardening" ${canInspect ? "" : "disabled"}>Verify</button>
	  <button type="button" data-action="refresh-hardening">Refresh</button>
	  <button type="button" class="${armed.has("hardening:remove") ? "confirm" : ""}" data-action="remove-hardening" ${canInspect ? "" : "disabled"}>${armed.has("hardening:remove") ? "Confirm remove" : "Remove"}</button>
	</div>
	${feedback(protectionFeedback, hardeningAlarm || !!applyBlocker, "Apply protection requests Windows approval, grants folder access, then tests the service identity.")}
	<div class="settings-subhead">Code signing</div>
	<p class="settings-note">Gives this installation a stable publisher identity and trusted local chain; it does not create Defender cloud reputation.</p>
	${row("certificate", `<span class="account-status"><span class="lamp ${certificateDone ? "live" : ""}"></span>${html(certificateDone ? `${signingStatus.subject} · ${signingStatus.thumbprint}` : "not done")}</span>`)}
	${row("artifacts", `<span class="account-status"><span class="lamp ${verifyDone ? "live" : signingStatus.loaded ? "alarm" : ""}"></span>${html(verifyDone ? "done · signed, timestamped, chain valid" : "not done")}</span>`)}
	${signingStatus.can_manage ? `<div class="settings-actions vertical">
	  <button type="button" data-action="create-signing" ${signingAllowed ? "" : "disabled"}>Create certificate</button>
	  <p class="settings-note">Create a protected certificate here, or import or select one you already own.</p>
	  ${row("PFX", '<input id="signing-pfx" type="file" accept=".pfx,application/x-pkcs12">')}
	  ${row("password", '<input id="signing-password" type="password" autocomplete="off">')}
	  ${row("stored certificate", `<select id="signing-thumbprint"><option value="">Select code-signing certificate</option>${(signingStatus.certificates || []).filter((certificate) => certificate.has_private_key).map((certificate) => `<option value="${attr(certificate.thumbprint)}" ${certificate.thumbprint === store.config.signing?.thumbprint ? "selected" : ""}>${html(certificate.subject)} · ${html(certificate.thumbprint)}</option>`).join("")}</select>`)}
	  <div class="settings-actions"><button type="button" data-action="import-signing" ${signingAllowed ? "" : "disabled"}>Import certificate</button><button type="button" data-action="export-signing" ${certificateDone && signingAllowed ? "" : "disabled"}>Export .cer</button></div>
	  <button type="button" data-action="sign-application" ${certificateDone && signingAllowed ? "" : "disabled"}>Sign application</button>
	  <p class="settings-note">Sign Agent_b.exe and PowerShell scripts, then restart Agent_b.</p>
	  <button type="button" data-action="verify-signing" ${signingBusy ? "disabled" : ""}>Verify signatures</button>
	  <p class="settings-note">Verify signer, thumbprint, timestamp, and certificate chain.</p>
	</div>` : `<div class="settings-actions vertical"><button type="button" data-action="verify-signing" ${signingBusy ? "disabled" : ""}>Verify signatures</button><p class="settings-note">Standard users can verify signatures but cannot create, import, select, export, or sign.</p></div>`}
	${feedback(signingMessage, signingAlarm, "Self-created keys are non-exportable and usable only by the elevated signing helper; imported keys keep their existing protection.")}
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
	const options = serverProfiles()
		.filter((profile) => !profileReason(profile))
		.map((profile) => `<option value="${attr(profile.id)}" ${profile.id === selected ? "selected" : ""}>${html(profile.label)} · ${html(profile.base_url)}</option>`)
		.join("");
	return options || '<option value="">No ready connection</option>';
}

function sessionControls(active) {
  if (!active) return '<p class="settings-note">No active session.</p>';
  const resetKey = `reset:${active.id}`;
  return `<div class="settings-actions vertical">
      <button type="button" class="${armed.has(resetKey) ? "confirm" : ""}" data-action="reset-session" data-id="${attr(active.id)}">${armed.has(resetKey) ? "Confirm clear" : "Clear conversation"}</button>
    </div>
    <p class="settings-note">Clears messages and run counters; keeps folder, profile, enabled tools, and memory.</p>
    ${copyRow("JSONL", active.log_path || "")}`;
}


export function renderSecurityPage(page, active, context) {
  useSettingsContext(context);
  return page === "session" ? sessionControls(active) : shell(active);
}
