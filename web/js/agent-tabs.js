export function agentTabLayout(count, width, minimumTabWidth = 72, overflowWidth = 36, gapWidth = 2) {
  const total = Math.max(0, Number(count) || 0);
  const available = Math.max(0, Number(width) || 0);
  if (!total || !available || total * minimumTabWidth + Math.max(0, total - 1) * gapWidth <= available) return { visible: total, hidden: 0 };
  const visible = Math.max(1, Math.min(total - 1, Math.floor((available - overflowWidth + gapWidth) / (minimumTabWidth + gapWidth))));
  return { visible, hidden: total - visible };
}
