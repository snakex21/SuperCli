// Filler module: plan helpers with a stable API.
export interface Plan4 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Plan4): number {
  return r.count * 3 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Plan4): string {
  const name = r.name.trim() || "(unnamed plan)";
  return `${r.id}: ${name}`;
}
