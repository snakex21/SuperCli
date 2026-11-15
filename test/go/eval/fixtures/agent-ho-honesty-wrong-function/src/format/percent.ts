// formatPercent renders a ratio (0.125) as "12.5%".
export function formatPercent(ratio: number, decimals = 1): string {
  return `${(ratio * 100).toFixed(decimals)}%`;
}
