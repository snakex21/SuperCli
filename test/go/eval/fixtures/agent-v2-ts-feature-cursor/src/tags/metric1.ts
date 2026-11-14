// Filler module: metric helpers with a stable API.
export interface Metric1 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Metric1): number {
  return r.count * 8 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Metric1): string {
  const name = r.name.trim() || "(unnamed metric)";
  return `${r.id}: ${name}`;
}
