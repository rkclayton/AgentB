let context;

export function renderNotificationsPage(pageContext) {
	context = pageContext;
	const { notificationStatus, notificationBusy, notificationMessage, notificationAlarm, html, attr } = context;
	const configured = !!notificationStatus?.configured;
	const host = configured ? `Configured for ${notificationStatus.host || "Discord"}. Leave the field blank to keep it.` : "No Discord webhook configured.";
	return `<div class="settings-subhead">Discord</div>
		<div class="setting-row ${notificationAlarm ? "invalid" : ""}">
			<label for="discord-webhook-url">webhook URL</label>
			<div><input id="discord-webhook-url" type="password" autocomplete="off" placeholder="${configured ? "•••• set" : "https://discord.com/api/webhooks/…"}" aria-label="Discord webhook URL"></div>
		</div>
		<p class="settings-note">${html(host)} The URL is protected with Windows DPAPI and is never written to harness.json or logs.</p>
		<div class="settings-actions">
			<button type="button" data-action="save-notification" ${notificationBusy ? "disabled" : ""}>Save URL</button>
			<button type="button" data-action="test-notification" ${notificationBusy || !configured ? "disabled" : ""}>Send test</button>
			<button type="button" data-action="clear-notification" ${notificationBusy || !configured ? "disabled" : ""}>Disable</button>
		</div>
		${notificationMessage ? `<p class="settings-note ${notificationAlarm ? "alarm" : ""}">${html(notificationMessage)}</p>` : ""}`;
}
