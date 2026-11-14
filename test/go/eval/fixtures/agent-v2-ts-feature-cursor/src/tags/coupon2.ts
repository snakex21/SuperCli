// Filler module: coupon helpers with a stable API.
export interface Coupon2 {
  id: string;
  name: string;
  count: number;
  tags: string[];
}

export function weight(r: Coupon2): number {
  return r.count * 6 + r.tags.reduce((n, t) => n + t.length, 0);
}

export function label(r: Coupon2): string {
  const name = r.name.trim() || "(unnamed coupon)";
  return `${r.id}: ${name}`;
}
