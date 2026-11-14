// Filler module: metric helpers with a stable API.
export interface Metric2 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Metric2): number {
  return r.count * 9 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Metric2): string {
  const name = r.name.trim() || "(unnamed metric)";
  return `${r.id}: ${name}`;
}
