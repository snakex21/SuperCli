// Filler module: webhook helpers with a stable API.
export interface Webhook2 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Webhook2): number {
  return r.count * 9 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Webhook2): string {
  const name = r.name.trim() || "(unnamed webhook)";
  return `${r.id}: ${name}`;
}
