import { test } from "node:test";
import assert from "node:assert/strict";
import { createClient } from "../net/client.js";
import { createClientPool } from "../net/pool.js";
import { allClients } from "../services/index.js";
import { authClient } from "../services/auth.js";

const pick = (c) => ({ host: c.host, port: c.port, timeoutMs: c.timeoutMs, retries: c.retries, tls: c.tls });

test("hidden: options object and defaults", () => {
  assert.deepEqual(pick(createClient({ host: "h" })), { host: "h", port: 80, timeoutMs: 5000, retries: 3, tls: false });
  assert.equal(createClient({ host: "h", tls: true }).port, 443);
  assert.equal(createClient({ host: "h", retries: 0 }).retries, 0);
  assert.equal(createClient({ host: "h", timeoutMs: 0 }).timeoutMs, 0);
  assert.equal(createClient({ host: "h", port: 8080, tls: true }).url("/x"), "https://h:8080/x");
  assert.equal(createClient({ host: "h", port: undefined, retries: undefined }).retries, 3);
  assert.throws(() => createClient({ port: 1 }));
  assert.throws(() => createClient({ host: "" }));
});

test("hidden: pool keeps positional signature", () => {
  const p = createClientPool(2, "r", 9000);
  assert.equal(p.length, 2);
  assert.deepEqual(pick(p[1]), { host: "r", port: 9000, timeoutMs: 5000, retries: 3, tls: false });
});

test("hidden: every service is configured as before", () => {
  const all = allClients({ host: "auth.internal" });
  assert.deepEqual(pick(all.billing), { host: "billing.internal", port: 8443, timeoutMs: 2000, retries: 0, tls: true });
  assert.deepEqual(pick(all.search), { host: "search.internal", port: 80, timeoutMs: 750, retries: 3, tls: false });
  assert.deepEqual(pick(all.geo), { host: "geo.internal", port: 80, timeoutMs: 5000, retries: 5, tls: false });
  assert.deepEqual(pick(all.mail), { host: "smtp-relay.internal", port: 2525, timeoutMs: 0, retries: 3, tls: false });
  assert.deepEqual(pick(all.auth), { host: "auth.internal", port: 80, timeoutMs: 5000, retries: 3, tls: false });
  assert.deepEqual(all.metrics.map(pick), [
    { host: "metrics.internal", port: 9091, timeoutMs: 5000, retries: 3, tls: true },
    { host: "metrics-replica.internal", port: 9091, timeoutMs: 5000, retries: 3, tls: false },
    { host: "metrics-replica.internal", port: 9091, timeoutMs: 5000, retries: 3, tls: false },
  ]);
  assert.deepEqual(pick(authClient({ host: "a", port: 1, timeoutMs: 0, retries: 0, tls: true })),
    { host: "a", port: 1, timeoutMs: 0, retries: 0, tls: true });
});
