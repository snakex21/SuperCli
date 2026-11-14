// Filler module: ledger helpers with a stable API.
export interface Ledger4 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Ledger4): number {
  return r.count * 7 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Ledger4): string {
  const name = r.name.trim() || "(unnamed ledger)";
  return `${r.id}: ${name}`;
}
