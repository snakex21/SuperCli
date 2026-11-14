// Filler module: quota helpers with a stable API.
export interface Quota2 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Quota2): number {
  return r.count * 3 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Quota2): string {
  const name = r.name.trim() || "(unnamed quota)";
  return `${r.id}: ${name}`;
}
