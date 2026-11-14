// Filler module: rule helpers with a stable API.
export interface Rule4 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Rule4): number {
  return r.count * 6 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Rule4): string {
  const name = r.name.trim() || "(unnamed rule)";
  return `${r.id}: ${name}`;
}
