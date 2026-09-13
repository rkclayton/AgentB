let store, choices, text;
function useSettingsContext(context) {
  ({ store, choices, text } = context);
}

function delivery() {
  const cfg = store.config.deliver || {};
  return `${choices("deliver.mode", "delivery", ["chips", "folder", "both"], cfg.mode || "both")}
    ${text("deliver.exchange_folder", "exchange folder", cfg.exchange_folder || "")}
    <p class="settings-note">The folder is created on first delivery. Apply host protections after changing it so the service identity receives Modify access only on this folder.</p>`;
}



export function renderDeliveryPage(context) {
  useSettingsContext(context);
  return delivery();
}
