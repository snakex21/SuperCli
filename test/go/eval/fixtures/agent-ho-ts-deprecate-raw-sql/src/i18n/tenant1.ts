// Filler module: tenant helpers with a stable API.
export interface Tenant1 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Tenant1): number {
  return r.count * 3 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Tenant1): string {
  const name = r.name.trim() || "(unnamed tenant)";
  return `${r.id}: ${name}`;
}
