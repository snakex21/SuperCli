// Filler module: bucket helpers with a stable API.
export interface Bucket3 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Bucket3): number {
  return r.count * 8 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Bucket3): string {
  const name = r.name.trim() || "(unnamed bucket)";
  return `${r.id}: ${name}`;
}
