// Filler module: region helpers with a stable API.
export interface Region1 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Region1): number {
  return r.count * 4 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Region1): string {
  const name = r.name.trim() || "(unnamed region)";
  return `${r.id}: ${name}`;
}
