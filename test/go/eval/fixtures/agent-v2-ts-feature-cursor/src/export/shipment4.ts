// Filler module: shipment helpers with a stable API.
export interface Shipment4 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Shipment4): number {
  return r.count * 3 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Shipment4): string {
  const name = r.name.trim() || "(unnamed shipment)";
  return `${r.id}: ${name}`;
}
