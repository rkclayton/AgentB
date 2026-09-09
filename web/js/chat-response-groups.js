import { groupAdjacentRuns } from "./timeline-groups.js";

export const thinThoughtTokenLimit = 64;

export function thoughtTokens(item) {
  if (Number.isFinite(Number(item?.reasoningTokens)) && Number(item.reasoningTokens) > 0) return Number(item.reasoningTokens);
  return Math.ceil(Array.from(item?.reasoning || "").length / 3.6);
}

export function isThinThought(item) {
  const tokens = thoughtTokens(item);
  return item?.type === "agent" && !item.text && tokens > 0 && tokens <= thinThoughtTokenLimit;
}

export function groupResponseRows(items = []) {
  return groupAdjacentRuns(
    items,
    (item) => item?.type === "tool" && item.name ? { key: item.name, id: item.key } : null,
    isThinThought,
  ).map((item) => item?.kind !== "adjacent-group" ? item : ({
    kind: "tool-group",
    key: `tool-group:${item.members[0].id}`,
    tool: item.groupKey,
    items: item.items,
    calls: item.members.length,
    thoughts: item.items.filter((entry) => isThinThought(entry)).length,
    failed: item.items.filter(itemFailed).length,
    duration: item.items.reduce((total, entry) => total + itemDuration(entry), 0),
  }));
}

export function responseSummary(items = []) {
  const tools = items.filter((item) => item?.type === "tool").length;
  const thoughts = items.filter((item) => item?.type === "agent" && (thoughtTokens(item) > 0 || !item.done)).length;
  const answers = items.filter((item) => item?.type === "agent" && item.text).length;
  const failed = items.filter(itemFailed).length;
  const duration = items.reduce((total, item) => total + itemDuration(item), 0);
  return { tools, thoughts, answers, failed, duration };
}

export function itemFailed(item) {
  if (!item || typeof item !== "object" || !item.key) return true;
  if (item.type === "tool") return !item.args || typeof item.args !== "object" || Array.isArray(item.args) || item.result?.ok === false;
  if (item.type !== "notice") return false;
  const event = item.event;
  if (!event || typeof event !== "object") return true;
  if (event.type === "run.aborted" || event.type === "workspace.conflict" || event.type === "error") return true;
  if (event.type === "run.stopped") return event.data?.reason && event.data.reason !== "done";
  if (event.type === "files.delivered") return (event.data?.items || []).some((value) => value.status === "failed");
  return event.data?.outcome === "error" || event.data?.decision === "deny";
}

function itemDuration(item) {
  if (item?.type === "tool") return Number(item.result?.ms || 0);
  if (item?.type === "agent") return Number(item.thinkingMS || 0);
  return 0;
}
