let store, toggle, number, approvalChoices;
function useSettingsContext(context) {
  ({ store, toggle, number, approvalChoices } = context);
}

function run() {
  const cfg = store.config;
  return `${toggle("chat.auto_rename", "auto-name chats every 20 turns", cfg.chat?.auto_rename !== false)}
    ${number("run.cycle_window", "cycle window", cfg.run?.cycle_window)}
    <p class="settings-note">0 = off</p>
    ${number("run.max_consecutive_tool_errors", "max tool errors", cfg.run?.max_consecutive_tool_errors)}
    <p class="settings-note">0 = off</p>
    ${approvalChoices(cfg.approval?.mode)}
    <p class="settings-note">With the service identity enabled, run_script still requires confirmation. Shell follows the approval mode; boundary-only runs in-folder commands silently, while boundary escapes and configured operator commands still ask.</p>
	${number("run.queue_depth", "queue depth (0 = unbounded)", cfg.run?.queue_depth)}`;
}


export function renderRunPage(context) {
  useSettingsContext(context);
  return run();
}
