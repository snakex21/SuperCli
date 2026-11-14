// Filler module: bucket helpers with a stable API.
export interface Bucket4 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Bucket4): number {
  return r.count * 9 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Bucket4): string {
  const name = r.name.trim() || "(unnamed bucket)";
  return `${r.id}: ${name}`;
}
