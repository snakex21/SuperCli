// Filler module: metric helpers with a stable API.
export interface Metric4 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Metric4): number {
  return r.count * 9 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Metric4): string {
  const name = r.name.trim() || "(unnamed metric)";
  return `${r.id}: ${name}`;
}
