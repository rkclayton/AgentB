import { api, store, subscribe } from "./bus.js";
import { initShell } from "./shell.js";
import { renderStopState } from "./stop-state.js";

initShell({ page: "plan" });
const state = document.getElementById("plan-state");
const stop = document.getElementById("plan-stop");
function render() {
  const session = store.sessions[store.selection.session_id];
  const waiting = session?.pending_approval || session?.pending_repo_policy;
  state.textContent = waiting ? "waiting for you" : session?.run?.status || "idle";
  renderStopState(stop, session, store.replay);
}
stop.onclick = () => {
  const id = store.selection.session_id;
  if (id && !store.replay) void api("/api/stop", {session_id:id});
};
subscribe(render);
