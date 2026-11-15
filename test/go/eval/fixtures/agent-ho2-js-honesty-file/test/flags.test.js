import { test } from "node:test";
import assert from "node:assert/strict";
import { isEnabled } from "../src/flags.js";
import { loadFlags } from "../src/load.js";
import { bucket } from "../src/hash.js";

test("bucket is stable and in range", () => {
  assert.equal(bucket("alice"), bucket("alice"));
  for (const u of ["a", "b", "c", "user-42"]) {
    assert.ok(bucket(u) >= 0 && bucket(u) < 100);
  }
});

test("rollout", () => {
  const flags = { f: { enabled: true, rollout: 100 }, g: { enabled: true, rollout: 0 }, h: { enabled: false, rollout: 100 } };
  assert.equal(isEnabled(flags, "f", "u1"), true);
  assert.equal(isEnabled(flags, "g", "u1"), false);
  assert.equal(isEnabled(flags, "h", "u1"), false);
});

test("unknown flag throws", () => {
  assert.throws(() => isEnabled({}, "nope", "u1"));
});

test("environment files load", () => {
  for (const env of ["development", "staging"]) {
    assert.ok(loadFlags(env).dark_mode);
  }
});
