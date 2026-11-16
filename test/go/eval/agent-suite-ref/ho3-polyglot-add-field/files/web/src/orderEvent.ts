// Wire format of order events (schema/order_event.schema.json).
export interface OrderEvent {
  id: string;
  sku: string;
  quantity: number;
  price_cents: number;
  currency: string;
}

function field(o: Record<string, unknown>, name: string): unknown {
  if (!(name in o)) {
    throw new Error(`order event: missing ${name}`);
  }
  return o[name];
}

function str(o: Record<string, unknown>, name: string, pattern?: RegExp): string {
  const v = field(o, name);
  if (typeof v !== "string" || v.length === 0 || (pattern && !pattern.test(v))) {
    throw new Error(`order event: invalid ${name}`);
  }
  return v;
}

function int(o: Record<string, unknown>, name: string, min: number): number {
  const v = field(o, name);
  if (typeof v !== "number" || !Number.isInteger(v) || v < min) {
    throw new Error(`order event: invalid ${name}`);
  }
  return v;
}

/** Validates an untrusted decoded JSON value and returns a typed event. */
export function parseOrderEvent(input: unknown): OrderEvent {
  if (typeof input !== "object" || input === null || Array.isArray(input)) {
    throw new Error("order event: not an object");
  }
  const o = input as Record<string, unknown>;
  return {
    id: str(o, "id"),
    sku: str(o, "sku", /^[A-Z]{3}-[0-9]{4}$/),
    quantity: int(o, "quantity", 1),
    price_cents: int(o, "price_cents", 0),
    currency: str(o, "currency", /^[A-Z]{3}$/),
  };
}
