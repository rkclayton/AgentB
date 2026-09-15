export const hintTexts = Object.freeze({
  accepted: "Cubes at the top track progress. Click one to reopen an item.",
  worker: "The worker runs this in a fresh session. You'll be asked only if it needs you.",
  apiAux: "Side jobs (compaction, naming) run on your API model. A small local Agent C does them for free. Assign one in Console.",
  noPlanner: "No planner assigned; agent_b is planning. Reviews are off.",
});

export function claimHint(storage, id) {
  const key = `agentb.plan.hint.${id}`;
  if (!hintTexts[id] || storage.getItem(key)) return "";
  storage.setItem(key, "1");
  return hintTexts[id];
}

export function planRows(text = "") {
  const lines = String(text).replaceAll("\r", "").split("\n");
  const order = section(lines, "## Current work order");
  const items = [];
  lines.forEach((line, index) => {
    const match = line.match(/^\s*\[([x~ !-])\]\s+(.+)$/);
    if (match) items.push({ marker: match[1], text: match[2], line, index });
  });
  return { order, items };
}

export function noteReports(text = "") {
  const lines = String(text).replaceAll("\r", "").split("\n");
  const reports = [];
  for (let index = 0; index < lines.length; index++) {
    if (!lines[index].startsWith("## ")) continue;
    let stop = lines.findIndex((line, candidate) => candidate > index && line.startsWith("## "));
    if (stop < 0) stop = lines.length;
    reports.push({ heading: lines[index].slice(3).trim(), body: lines.slice(index + 1, stop).join("\n").trim() });
  }
  return reports;
}

function section(lines, heading) {
  const start = lines.findIndex((line) => line.trim() === heading);
  if (start < 0) return "";
  let end = lines.findIndex((line, index) => index > start && /^##\s/.test(line));
  if (end < 0) end = lines.length;
  return lines.slice(start + 1, end).join("\n").trim();
}

export function proposalLabel(proposal) { return `${proposal.kind} · ${proposal.path} · ${proposal.item_id}`; }
export function proposalKey(proposal) { return JSON.stringify([proposal.id, proposal.kind, proposal.path, proposal.old_text, proposal.new_text, proposal.item_id]); }
