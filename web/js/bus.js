export const store = {
  sessions: {},
  active: "",
  servers: [],
  config: {},
  flow: { stages: [], edges: [] },
  tools: [],
  serving_facts: {},
	shell_credential: { stored: false, stored_at: "" },
	shell_identity: { fallback: false, reason: "", since: "" },
  replay: false,
};
const listeners = new Set();
export function subscribe(fn) {
  listeners.add(fn);
  fn(store, { type: "init" });
  return () => listeners.delete(fn);
}
function notify(event) {
  for (const fn of listeners) fn(store, event);
}
function session(event) {
  return store.sessions[event.session_id];
}
function mergeRun(target, patch) {
  target.run = Object.assign({}, target.run, patch);
}
// Keep event-state changes synchronized with internal/events/ReduceReplay.
export function reduce(event) {
  const data = event.data || {};
  if (event.type === "snapshot") {
    const active = store.active;
    Object.assign(store, data);
    store.active = store.sessions[active]
      ? active
      : Object.keys(store.sessions)[0] || "";
    for (const value of Object.values(store.sessions)) hydrate(value);
    notify(event);
    return;
  }
  const target = session(event);
  switch (event.type) {
    case "session.created":
      store.sessions[data.session.id] = data.session;
	  if (store.replay) store.sessions[data.session.id].run.status = "replay";
      hydrate(data.session);
      if (!store.active) store.active = data.session.id;
      break;
    case "session.renamed":
      if (target) target.label = data.label;
      break;
    case "session.reset":
      if (target) {
        target.messages = [];
        target.timeline = [];
        mergeRun(target, { status: "idle", turn: 0, partial: "" });
      }
      break;
    case "session.closed":
	  if (!store.replay) delete store.sessions[data.session_id];
      if (store.active === data.session_id)
        store.active = Object.keys(store.sessions)[0] || "";
      break;
    case "server.probed": {
      const p = store.servers.find((x) => x.id === data.server_id);
      if (p) {
        p.capabilities = data.capabilities;
        p.reasoning.valid_efforts = data.capabilities?.valid_efforts || [];
        p._probing = false;
      }
	  const configured = store.config.servers?.find((x) => x.id === data.server_id);
	  if (configured) {
		configured.capabilities = data.capabilities;
		configured.reasoning.valid_efforts = data.capabilities?.valid_efforts || [];
	  }
      break;
    }
    case "config.changed":
      store.config = data.config;
      store.servers = data.config.servers || store.servers;
      break;
	case "shell.identity":
		store.shell_identity = data;
		break;
	case "shell.credential":
		store.shell_credential = data;
		break;
    case "error":
      store.error = data;
      break;
    case "run.queued":
      if (target)
        mergeRun(target, {
		  status: store.replay ? "replay" : "queued",
          run_id: data.run_id,
          queue_position: data.position,
        });
      break;
    case "run.started":
      if (target) {
        mergeRun(target, {
		  status: store.replay ? "replay" : "running",
          run_id: data.run_id,
          turn: 0,
          queue_position: 0,
        });
        target.queued_messages = Math.max(0, (target.queued_messages || 0) - 1);
        target._lastStop = "";
        target._dispatchAlarm = false;
      }
      break;
    case "run.stopped":
      if (target) {
		mergeRun(target, { status: store.replay ? "replay" : "idle", partial: "" });
        target._lastStop = data.reason;
        target._stage = "wait_user";
        if (data.reason === "tool_errors") target._dispatchAlarm = true;
      }
      break;
    case "stage":
      if (target) {
        target._stage = data.state === "enter" ? data.stage : target._stage;
        target._stageState = data.state;
        if (data.state === "enter" && data.stage === "assemble")
          target._completedStages = [];
        else if (
          data.state === "exit" &&
          !target._completedStages.includes(data.stage)
        )
          target._completedStages.push(data.stage);
        target.run.turn = data.turn;
      }
      break;
    case "model.progress":
      if (target) target._progress = data;
      break;
    case "model.delta":
      if (target && data.kind === "content")
        target.run.partial = (target.run.partial || "") + data.text;
      break;
    case "model.response":
      if (target) {
        target._timings = data.timings;
        target.run.partial = "";
      }
      break;
    case "tool.call":
      if (target) target._activeTool = data.name;
      break;
    case "tool.result":
      if (target) {
        target._activeTool = "";
        const tool = target.tools.find((x) => x.name === data.name);
        if (tool) tool.calls = (tool.calls || 0) + 1;
      }
      break;
    case "tool.toggled":
      if (target) {
        const tool = target.tools.find((x) => x.name === data.name);
        if (tool) tool.enabled = data.enabled;
      }
      break;
    case "message.appended":
      if (target) {
        if (data.message?.category === "summary")
          target.messages.splice(1, 0, data.message);
        else target.messages.push(data.message);
      }
      break;
    case "message.updated":
      if (target) {
        const m = target.messages.find((x) => x.id === data.id);
        if (m) Object.assign(m, data.patch);
      }
      break;
    case "message.queued":
      if (target) target.queued_messages = (target.queued_messages || 0) + 1;
      break;
    case "budget":
      if (target) target.budget = data;
      break;
    case "approval.required":
      if (target) mergeRun(target, { status: "paused" });
      break;
    case "approval.decided":
      if (target) mergeRun(target, { status: "running" });
      break;
    case "cycle.detected":
      if (target) target._dispatchAlarm = true;
      break;
    case "workspace.conflict":
      if (target) {
        target._alarmTool = target._activeTool;
        const alarmTarget = target;
        setTimeout(() => {
          alarmTarget._alarmTool = "";
          notify({ type: "rack.alarm.cleared", session_id: alarmTarget.id });
        }, 600);
      }
      break;
    case "compaction":
      if (target && data.kind === "summarize") {
        const removed = new Set(data.affected_ids || []);
        target.messages = target.messages.filter(
          (message) => !removed.has(message.id),
        );
      }
      if (target) {
        target._compacted = true;
        const compactedTarget = target;
        setTimeout(() => {
          compactedTarget._compacted = false;
          notify({
            type: "rail.compaction.done",
            session_id: compactedTarget.id,
          });
        }, 160);
      }
      break;
    case "memory.noted":
	  if (target) {
		target.memory_path = data.path || target.memory_path;
		const line = `- ${data.note}`;
		target.memory_content = target.memory_content
		  ? `${target.memory_content.trimEnd()}\n${line}\n`
		  : `${line}\n`;
	  }
      break;
  }
  if (target) {
    target.timeline = target.timeline || [];
    target.timeline.push(event);
    if (!store.replay && target.timeline.length > 500)
      target.timeline = target.timeline.slice(-500);
  }
  if (event.type === "workspace.conflict" && data.other_session_id) {
    const other = store.sessions[data.other_session_id];
    if (other && other !== target) {
      other.timeline = other.timeline || [];
      other.timeline.push(event);
      if (other.timeline.length > 500)
        other.timeline = other.timeline.slice(-500);
    }
  }
  notify(event);
}
function hydrate(value) {
  value.messages = value.messages || [];
  value.timeline = value.timeline || [];
  value.tools = value.tools || [];
	value._stage = "";
	value._completedStages = [];
	value._activeTool = "";
	for (const event of value.timeline) {
	  const data = event.data || {};
	  if (event.type === "stage") {
		if (data.state === "enter") {
		  value._stage = data.stage;
		  if (data.stage === "assemble") value._completedStages = [];
		} else if (!value._completedStages.includes(data.stage)) value._completedStages.push(data.stage);
	  } else if (event.type === "model.progress") value._progress = data;
	  else if (event.type === "model.response") value._timings = data.timings;
	  else if (event.type === "tool.call") value._activeTool = data.name;
	  else if (event.type === "tool.result") value._activeTool = "";
	  else if (event.type === "run.stopped") {
		value._lastStop = data.reason;
		value._stage = "wait_user";
		value._stageState = "enter";
	  }
	}
}
export function setActive(id) {
  if (store.sessions[id]) {
    store.active = id;
    notify({ type: "active.changed", data: { id } });
  }
}
export async function api(path, body, method = "POST") {
  const options = { method, headers: {} };
  if (body !== undefined) {
    options.headers["Content-Type"] = "application/json";
    options.body = JSON.stringify(body);
  }
  const response = await fetch(path, options);
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
	const error = new Error(data.error || `HTTP ${response.status}`);
	error.field = data.field || "";
	error.status = response.status;
	error.data = data;
	throw error;
  }
  return data;
}
const connection = document.getElementById("connection");
let reconnected = false;
const source = new EventSource("/api/events");
source.onopen = () => {
  if (reconnected && connection) {
    connection.textContent = "reconnected";
    connection.className = "";
    setTimeout(() => {
      connection.textContent = "";
    }, 3000);
  }
  reconnected = true;
};
source.onerror = () => {
  if (connection) {
    connection.textContent = "connection lost — retrying";
    connection.className = "alarm";
  }
};
source.onmessage = (event) => reduce(JSON.parse(event.data));
for (const type of [
  "snapshot",
  "session.created",
  "session.renamed",
  "session.reset",
  "session.closed",
  "server.probed",
  "config.changed",
	"shell.identity",
	"shell.credential",
  "error",
  "run.queued",
  "run.started",
  "run.stopped",
  "stage",
  "model.request",
  "model.progress",
  "model.delta",
  "model.response",
  "tool.call",
  "tool.result",
  "tool.toggled",
  "message.appended",
  "message.updated",
  "message.queued",
  "budget",
  "approval.required",
  "approval.decided",
  "cycle.detected",
  "workspace.conflict",
  "compaction",
  "memory.noted",
]) {
  source.addEventListener(type, (event) => reduce(JSON.parse(event.data)));
}
