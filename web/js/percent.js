export function percentClass(value) {
  const numeric = Number(value);
  const bounded = Number.isFinite(numeric) ? Math.max(0, Math.min(100, numeric)) : 0;
  return `pct-${Math.round(bounded * 4)}`;
}
