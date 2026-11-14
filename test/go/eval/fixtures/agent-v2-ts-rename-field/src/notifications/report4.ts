// Filler module: report helpers with a stable API.
export interface Report4 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Report4): number {
  return r.count * 4 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Report4): string {
  const name = r.name.trim() || "(unnamed report)";
  return `${r.id}: ${name}`;
}
