// Filler module: ticket helpers with a stable API.
export interface Ticket2 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Ticket2): number {
  return r.count * 8 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Ticket2): string {
  const name = r.name.trim() || "(unnamed ticket)";
  return `${r.id}: ${name}`;
}
