// Filler module: batch helpers with a stable API.
export interface Batch1 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Batch1): number {
  return r.count * 9 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Batch1): string {
  const name = r.name.trim() || "(unnamed batch)";
  return `${r.id}: ${name}`;
}
