export function liveActivityText(session) {
  if (session?.run?.status !== "running") return "";
  const activity = session.activity || {};
  if (activity.stage_state === "exit") {
    switch (activity.stage) {
      case "assemble": return "waiting for model";
      case "call_model": return "waiting for response parsing";
      case "parse": return "waiting for next action";
      case "dispatch": return "waiting for tool execution";
      case "execute": return "waiting for result recording";
      case "append": return "waiting for context check";
      case "compact": return "waiting for next turn";
      case "wait_user": return "waiting to resume";
      default: return "waiting · state unknown";
    }
  }
  if (activity.stage_state !== "enter") return "waiting · state unknown";
  switch (activity.stage) {
    case "assemble": return "assembling turn";
    case "call_model": return activity.stream?.has_chunk ? "model producing" : "waiting for model";
    case "parse": return "parsing model response";
    case "dispatch": return "preparing tool call";
    case "execute": return activity.active_tool ? `tool executing · ${activity.active_tool}` : "tool executing · unknown";
    case "append": return "recording tool result";
    case "compact": return "compacting context";
    case "wait_user": return "waiting for you";
    default: return "waiting · state unknown";
  }
}

export function showsStreamCaret(entry) {
  return entry?.type === "agent" && !entry.done && !!entry.text;
}
