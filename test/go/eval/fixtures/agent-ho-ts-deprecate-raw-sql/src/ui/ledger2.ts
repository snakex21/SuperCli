// Filler module: ledger helpers with a stable API.
export interface Ledger2 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Ledger2): number {
  return r.count * 3 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Ledger2): string {
  const name = r.name.trim() || "(unnamed ledger)";
  return `${r.id}: ${name}`;
}
