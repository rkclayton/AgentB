export function formatDuration(milliseconds) {
  if (milliseconds === undefined || milliseconds === null || !Number.isFinite(Number(milliseconds))) return "";
  const value = Math.max(0, Number(milliseconds));
  return value >= 1000 ? `${(value / 1000).toFixed(1)} s` : `${Math.round(value)} ms`;
}
