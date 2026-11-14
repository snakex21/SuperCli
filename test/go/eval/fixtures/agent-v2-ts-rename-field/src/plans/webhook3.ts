// Filler module: webhook helpers with a stable API.
export interface Webhook3 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Webhook3): number {
  return r.count * 3 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Webhook3): string {
  const name = r.name.trim() || "(unnamed webhook)";
  return `${r.id}: ${name}`;
}
