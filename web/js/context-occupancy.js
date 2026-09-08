const categoryGroups = [
  { key: "system", label: "system prompt", sources: ["system"] },
  { key: "project", label: "project instructions", sources: ["project"] },
  { key: "schemas", label: "tool schemas", sources: ["tools"] },
  { key: "tool-results", label: "tool results", sources: ["files", "results", "fetched"] },
  { key: "history", label: "history", sources: ["history", "summary"] },
  { key: "workspace-memory", label: "workspace memory", sources: ["workspace_memory", "memory"] },
  { key: "agent-memory", label: "agent memory", sources: ["agent_memory"] },
];

export function contextOccupancy(budget = {}) {
  const categories = budget.categories || {};
  const explicitlyEstimated = new Set(budget.estimated_categories || []);
  const allSourcesEstimated = !!budget.estimated && explicitlyEstimated.size === 0;
  const items = categoryGroups.map((group) => ({
    key: group.key,
    label: group.label,
    value: group.sources.reduce((total, source) => total + positive(categories[source]), 0),
    estimated:
      allSourcesEstimated || group.sources.some((source) => explicitlyEstimated.has(source)),
  }));
  const occupied = items.reduce((total, item) => total + item.value, 0);
  const reserve = positive(budget.reserve);
  const nctx = positive(budget.n_ctx);
  const anyEstimated = items.some((item) => item.estimated);
  items.push(
    {
      key: "free",
      label: "free",
      value: Math.max(0, nctx - occupied - reserve),
      estimated: anyEstimated,
    },
    { key: "reserve", label: "reserve", value: reserve, estimated: false },
  );
  return { items, occupied, nctx, estimated: anyEstimated };
}

function positive(value) {
  const number = Number(value);
  return Number.isFinite(number) ? Math.max(0, number) : 0;
}
