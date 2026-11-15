// Filler module: webhook helpers with a stable API.
export interface Webhook1 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Webhook1): number {
  return r.count * 5 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Webhook1): string {
  const name = r.name.trim() || "(unnamed webhook)";
  return `${r.id}: ${name}`;
}
