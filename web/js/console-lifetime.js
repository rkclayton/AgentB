export function agentKey(agent) {
  return String(agent?.name || "").trim().toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
}

export function ratio(part, whole) {
  return whole > 0 ? `${(100 * part / whole).toFixed(1)}%` : "0.0%";
}

export function duration(milliseconds) {
  const ms = Number(milliseconds || 0);
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)} s`;
  return `${(ms / 60000).toFixed(1)} min`;
}

export function lifetimeRows(counters = {}, percentile = () => 0) {
  const reliability = counters.worker_reliability || {};
  const briefs = Number(reliability.briefs || counters.runs || 0);
  const tokens = Number(counters.prompt_tokens || 0) + Number(counters.completion_tokens || 0);
  const responses = counters.model_response_ms || [];
  return [
    ["chats", counters.chats || 0], ["runs / briefs", counters.runs || 0], ["turns", counters.turns || 0],
    ["approvals raised / approved", `${counters.approvals_raised || 0} / ${counters.approvals_approved || 0}`],
    ["operator grants", counters.operator_grants || 0], ["compactions", counters.compactions || 0],
    ["prompt / completion tokens", `${counters.prompt_tokens || 0} / ${counters.completion_tokens || 0}`],
    ["cache-hit ratio", ratio(counters.cached_tokens || 0, counters.prompt_tokens || 0)],
    ["wall time", duration(counters.wall_ms)], ["model response p50 / max", `${duration(percentile(responses, .5))} / ${duration(Math.max(0, ...responses))}`],
    ["first / last use", `${counters.first_use || "—"} / ${counters.last_use || "—"}`],
    ["completion rate", ratio(reliability.completed || 0, briefs)],
    ["interventions / brief", briefs ? (Number(reliability.interventions || 0) / briefs).toFixed(2) : "0.00"],
    ["cost / brief", briefs ? `${(Number(counters.turns || 0) / briefs).toFixed(2)} turns · ${Math.round(tokens / briefs)} tokens · ${duration(Number(counters.wall_ms || 0) / briefs)} · ${ratio(counters.cached_tokens || 0, counters.prompt_tokens || 0)} cache` : "0 turns · 0 tokens · 0 ms · 0.0% cache"],
    ["failure attribution model | harness | brief", `${reliability.model_failures || 0} | ${reliability.harness_failures || 0} | ${reliability.brief_failures || 0}`],
    ["rework rate", ratio(reliability.reworked || 0, briefs)], ["silence rate", ratio(reliability.silent || 0, briefs)],
  ];
}
