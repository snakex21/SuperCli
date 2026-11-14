// Filler module: plan helpers with a stable API.
export interface Plan3 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Plan3): number {
  return r.count * 8 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Plan3): string {
  const name = r.name.trim() || "(unnamed plan)";
  return `${r.id}: ${name}`;
}
