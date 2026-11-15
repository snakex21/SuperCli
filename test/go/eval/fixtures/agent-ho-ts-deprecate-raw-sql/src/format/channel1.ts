// Filler module: channel helpers with a stable API.
export interface Channel1 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Channel1): number {
  return r.count * 5 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Channel1): string {
  const name = r.name.trim() || "(unnamed channel)";
  return `${r.id}: ${name}`;
}
