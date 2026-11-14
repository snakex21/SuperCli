// Filler module: rule helpers with a stable API.
export interface Rule1 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Rule1): number {
  return r.count * 5 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Rule1): string {
  const name = r.name.trim() || "(unnamed rule)";
  return `${r.id}: ${name}`;
}
