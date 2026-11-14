// Filler module: coupon helpers with a stable API.
export interface Coupon3 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Coupon3): number {
  return r.count * 9 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Coupon3): string {
  const name = r.name.trim() || "(unnamed coupon)";
  return `${r.id}: ${name}`;
}
