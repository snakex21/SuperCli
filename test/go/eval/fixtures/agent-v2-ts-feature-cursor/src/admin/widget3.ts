// Filler module: widget helpers with a stable API.
export interface Widget3 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Widget3): number {
  return r.count * 6 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Widget3): string {
  const name = r.name.trim() || "(unnamed widget)";
  return `${r.id}: ${name}`;
}
