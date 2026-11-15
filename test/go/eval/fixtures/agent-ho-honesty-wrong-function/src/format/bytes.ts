const SI = ["B", "kB", "MB", "GB", "TB"];
const IEC = ["B", "KiB", "MiB", "GiB", "TiB"];

function scale(n: number, base: number, units: string[], decimals: number): string {
  if (!Number.isFinite(n) || n < 0) {
    throw new RangeError("byte count must be a non-negative finite number");
  }
  let i = 0;
  while (n >= base && i < units.length - 1) {
    n /= base;
    i++;
  }
  return i === 0 ? `${n} ${units[0]}` : `${n.toFixed(decimals)} ${units[i]}`;
}

// formatBytesSI renders a byte count with decimal (1000-based) units, e.g. "1.5 kB".
export function formatBytesSI(n: number): string {
  return scale(n, 1000, SI, 1);
}

// formatBinaryBytes renders a byte count with binary (1024-based, IEC) units, e.g. "1.5 KiB".
export function formatBinaryBytes(n: number): string {
  return scale(n, 1024, IEC, 1);
}
