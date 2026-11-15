// Filler module: ledger helpers with a stable API.
export interface Ledger1 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Ledger1): number {
  return r.count * 6 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Ledger1): string {
  const name = r.name.trim() || "(unnamed ledger)";
  return `${r.id}: ${name}`;
}
