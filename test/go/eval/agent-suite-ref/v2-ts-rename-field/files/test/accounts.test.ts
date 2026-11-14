import { test } from "node:test";
import assert from "node:assert/strict";
import { newAccount, isPaid } from "../src/models/account.ts";
import { toWire, fromWire } from "../src/wire/serialize.ts";
import { findByUser } from "../src/services/lookup.ts";

test("account basics", () => {
  const a = newAccount("u1", "a@x", "pro");
  assert.equal(a.accountId, "u1");
  assert.ok(isPaid(a));
  assert.deepEqual(fromWire(toWire(a)), a);
  assert.equal(findByUser([a], "u1"), a);
});
