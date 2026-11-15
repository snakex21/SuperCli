import { test } from "node:test";
import assert from "node:assert/strict";
import { createClient } from "../net/client.js";
import { createClientPool } from "../net/pool.js";
import { allClients } from "../services/index.js";

test("defaults", () => {
  const c = createClient("h");
  assert.equal(c.port, 80);
  assert.equal(c.timeoutMs, 5000);
  assert.equal(c.retries, 3);
  assert.equal(c.url("/x"), "http://h:80/x");
});

test("tls default port", () => {
  assert.equal(createClient("h", undefined, undefined, undefined, true).port, 443);
});

test("host required", () => {
  assert.throws(() => createClient(""), TypeError);
});

test("pool", () => {
  const p = createClientPool(3, "h", 81);
  assert.equal(p.length, 3);
  assert.equal(p[2].port, 81);
});

test("services build", () => {
  const all = allClients({ host: "auth.internal", tls: true });
  assert.equal(all.auth.port, 443);
  assert.equal(all.metrics.length, 3);
});
