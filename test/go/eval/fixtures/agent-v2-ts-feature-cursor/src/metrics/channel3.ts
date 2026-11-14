// Filler module: channel helpers with a stable API.
export interface Channel3 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Channel3): number {
  return r.count * 6 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Channel3): string {
  const name = r.name.trim() || "(unnamed channel)";
  return `${r.id}: ${name}`;
}
