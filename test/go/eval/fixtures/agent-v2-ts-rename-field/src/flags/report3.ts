// Filler module: report helpers with a stable API.
export interface Report3 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Report3): number {
  return r.count * 7 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Report3): string {
  const name = r.name.trim() || "(unnamed report)";
  return `${r.id}: ${name}`;
}
