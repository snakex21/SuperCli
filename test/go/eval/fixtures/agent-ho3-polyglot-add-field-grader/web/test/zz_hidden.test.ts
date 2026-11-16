import test from "node:test";
import assert from "node:assert/strict";
import { parseOrderEvent } from "../src/orderEvent.ts";

const base = { id: "o-1", sku: "ABC-0001", quantity: 2, price_cents: 1999 };

test("hidden: currency is parsed", () => {
  assert.equal((parseOrderEvent({ ...base, currency: "EUR" }) as any).currency, "EUR");
});

test("hidden: missing or malformed currency is rejected", () => {
  assert.throws(() => parseOrderEvent({ ...base }));
  for (const c of ["eur", "EURO", "EU", "", 978, null]) {
    assert.throws(() => parseOrderEvent({ ...base, currency: c }), String(c));
  }
});
