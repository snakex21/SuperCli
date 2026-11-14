// Filler module: profile helpers with a stable API.
export interface Profile3 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Profile3): number {
  return r.count * 5 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Profile3): string {
  const name = r.name.trim() || "(unnamed profile)";
  return `${r.id}: ${name}`;
}
