// Filler module: widget helpers with a stable API.
export interface Widget4 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Widget4): number {
  return r.count * 4 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Widget4): string {
  const name = r.name.trim() || "(unnamed widget)";
  return `${r.id}: ${name}`;
}
