// Filler module: ticket helpers with a stable API.
export interface Ticket4 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Ticket4): number {
  return r.count * 9 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Ticket4): string {
  const name = r.name.trim() || "(unnamed ticket)";
  return `${r.id}: ${name}`;
}
