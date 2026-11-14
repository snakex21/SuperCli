import { test } from "node:test";
import assert from "node:assert/strict";
import { newAccount } from "../src/models/account.ts";
import { toWire, fromWire } from "../src/wire/serialize.ts";
import { record, byUser } from "../src/audit/audit.ts";
import { createSession } from "../src/session/session.ts";
import { invoiceKey, invoiceKeys } from "../src/billing/invoice.ts";
import { findByUser, indexByUser } from "../src/services/lookup.ts";
import { mergeAccounts } from "../src/services/merge.ts";
import { handleGet } from "../src/api/handlers.ts";

const a = newAccount("u1", "a@x", "team", "2026-02-02T00:00:00Z");
const b = newAccount("u2", "b@x", "pro", "2026-03-03T00:00:00Z");

test("Account uses accountId", () => {
  assert.deepEqual(a, { accountId: "u1", email: "a@x", plan: "team", createdAt: "2026-02-02T00:00:00Z" });
  assert.ok(!("userId" in a));
});

test("wire format unchanged", () => {
  assert.deepEqual(toWire(a), { user_id: "u1", email: "a@x", plan: "team", created_at: "2026-02-02T00:00:00Z" });
  const back = fromWire({ user_id: "u9", email: "e", plan: "free", created_at: "t" });
  assert.deepEqual(back, { accountId: "u9", email: "e", plan: "free", createdAt: "t" });
});

test("other userId properties keep their name", () => {
  const e = record(a, "login", "web");
  assert.deepEqual(e, { userId: "u1", action: "login", target: "web" });
  assert.equal(byUser([e], "u1").length, 1);
  assert.deepEqual(createSession(b, "tok"), { userId: "u2", token: "tok" });
});

test("services follow the rename", () => {
  assert.equal(invoiceKey(a, 3), "inv-u1-3");
  assert.deepEqual(invoiceKeys([a, b], 1), { u1: "inv-u1-1", u2: "inv-u2-1" });
  assert.equal(findByUser([a, b], "u2"), b);
  assert.equal(indexByUser([a, b]).get("u1"), a);
  const m = mergeAccounts(a, b);
  assert.deepEqual(m, { accountId: "u1", email: "b@x", plan: "pro", createdAt: "2026-03-03T00:00:00Z" });
  assert.deepEqual(handleGet({ query: { user_id: "u2" } }, [a, b]), { status: 200, body: toWire(b) });
  assert.equal(handleGet({ query: { user_id: "zz" } }, [a, b]).status, 404);
});
