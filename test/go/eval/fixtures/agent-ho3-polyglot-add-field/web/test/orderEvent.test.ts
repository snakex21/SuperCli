import test from "node:test";
import assert from "node:assert/strict";
import { parseOrderEvent } from "../src/orderEvent.ts";

const valid = { id: "o-1", sku: "ABC-0001", quantity: 2, price_cents: 1999, currency: "PLN" };

test("parses a valid event", () => {
  const e = parseOrderEvent(valid);
  assert.equal(e.sku, "ABC-0001");
  assert.equal(e.price_cents, 1999);
});

test("rejects a bad sku", () => {
  assert.throws(() => parseOrderEvent({ ...valid, sku: "abc-1" }));
});

test("rejects a non-object", () => {
  assert.throws(() => parseOrderEvent([valid]));
});
