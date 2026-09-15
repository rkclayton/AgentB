const activeStates = new Set(["running", "queued", "paused", "stopping", "held"]);

export function openSessions(sessions = {}) {
  return Object.values(sessions).filter((session) => !session.closed);
}

export function firstUserLine(session) {
  const text = (session?.chat || []).find((entry) => entry.type === "user")?.text || "New chat";
  return text.split(/\r?\n/, 1)[0].trim() || "New chat";
}

export function sessionTitle(session) {
  if (!session) return "";
  const profile = session.b_profile || session.server_id || "profile";
  return `agent_${session.role === "d" ? "d" : "b"} · ${profile}`;
}

export function agentAuthor(session, role = "b") {
  const letter = ["b", "c", "d"].includes(role) ? role : role === "aux" ? "c" : "b";
  return `agent_${letter}`;
}

export function runCount(session) {
  const ids = new Set();
  for (const event of session?.timeline || []) {
    if (event.type === "run.started") ids.add(event.run_id || event.data?.run_id);
  }
  ids.delete("");
  ids.delete(undefined);
  return ids.size;
}

export function relativeTime(value, now = Date.now()) {
  const at = Date.parse(value || "");
  if (!Number.isFinite(at)) return "now";
  const seconds = Math.max(0, Math.floor((now - at) / 1000));
  if (seconds < 60) return "now";
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  return `${Math.floor(seconds / 86400)}d`;
}

export function stateGlyph(session) {
  const state = session?.run?.status || "idle";
  if (state === "running") return "●";
  if (state === "queued") return "◌";
  if (state === "paused") return "Ⅱ";
  if (state === "stopping") return "■";
  if (state === "held") return "◇";
  return "○";
}

export function chatRowText(session, now = Date.now(), limit = 64) {
  const line = firstUserLine(session);
  const title = line.length > limit ? `${line.slice(0, Math.max(1, limit - 1))}…` : line;
  const runs = runCount(session);
  return `${relativeTime(session?.created_at, now)} · ${title} · ${runs} ${runs === 1 ? "run" : "runs"} · ${stateGlyph(session)}`;
}

export function isRunning(session) {
  return activeStates.has(session?.run?.status);
}
