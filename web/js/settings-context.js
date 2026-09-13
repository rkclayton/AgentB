let store, serverProfiles, number, choices, row, html;
function useSettingsContext(context) {
  ({ store, serverProfiles, number, choices, row, html } = context);
}

function context(active) {
  const profile = serverProfiles().find((x) => x.id === active?.server_id);
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


export function renderContextPage(active, pageContext) {
  useSettingsContext(pageContext);
  return context(active);
}
