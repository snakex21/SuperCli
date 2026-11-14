// Filler module: region helpers with a stable API.
export interface Region2 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Region2): number {
  return r.count * 5 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Region2): string {
  const name = r.name.trim() || "(unnamed region)";
  return `${r.id}: ${name}`;
}
