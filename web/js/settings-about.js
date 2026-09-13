let store, row, html;
function useSettingsContext(context) {
  ({ store, row, html } = context);
}

function about() {
  const build = store.build || {};
  const tag = build.tag ? (String(build.tag).startsWith("v") ? build.tag : `v${build.tag}`) : "version unknown";
  const commit = String(build.commit || "unknown").slice(0, 7);
  return `${row("version", `<code>${html(`${tag} · ${commit}${build.dirty ? " · dirty" : ""}`)}</code>`)}
    <p class="settings-note">Build and signing details are kept in Settings so the shared application shell stays focused on selection and run state.</p>`;
}


export function renderAboutPage(context) {
  useSettingsContext(context);
  return about();
}
