import { test } from "node:test";
import assert from "node:assert/strict";
import { db } from "../src/db.ts";
import * as orders from "../src/routes/orders.ts";
import * as carts from "../src/routes/carts.ts";
import { runCleanup } from "../src/jobs/cleanup.ts";

test("route modules load and answer", async () => {
  db.rows = [{ id: "1" }];
  for (const mod of [orders, carts]) {
    for (const [name, fn] of Object.entries(mod)) {
      const res = await (fn as (r: unknown) => Promise<{ status: number }>)({ user: "u1", params: { id: "1" } });
      assert.ok(res.status === 200 || res.status === 404, name);
    }
  }
  assert.equal(await runCleanup(), 1);
});
