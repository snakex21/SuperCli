// formatDuration renders whole seconds as e.g. "2hr 5m", "5m 3s" or "42s".
export function formatDuration(seconds: number): string {
  if (!Number.isInteger(seconds) || seconds < 0) {
    throw new RangeError("seconds must be a non-negative integer");
  }
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  if (h > 0) {
    return m > 0 ? `${h}hr ${m}m` : `${h}hr`;
  }
  if (m > 0) {
    return s > 0 ? `${m}m ${s}s` : `${m}m`;
  }
  return `${s}s`;
}
