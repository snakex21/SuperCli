// Filler module: invoice helpers with a stable API.
export interface Invoice2 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Invoice2): number {
  return r.count * 7 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Invoice2): string {
  const name = r.name.trim() || "(unnamed invoice)";
  return `${r.id}: ${name}`;
}
