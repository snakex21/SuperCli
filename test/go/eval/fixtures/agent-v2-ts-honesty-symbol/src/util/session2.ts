// Filler module: session helpers with a stable API.
export interface Session2 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Session2): number {
  return r.count * 6 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Session2): string {
  const name = r.name.trim() || "(unnamed session)";
  return `${r.id}: ${name}`;
}
