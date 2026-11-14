// Filler module: route helpers with a stable API.
export interface Route4 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Route4): number {
  return r.count * 5 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Route4): string {
  const name = r.name.trim() || "(unnamed route)";
  return `${r.id}: ${name}`;
}
