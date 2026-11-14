// Filler module: route helpers with a stable API.
export interface Route3 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Route3): number {
  return r.count * 9 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Route3): string {
  const name = r.name.trim() || "(unnamed route)";
  return `${r.id}: ${name}`;
}
