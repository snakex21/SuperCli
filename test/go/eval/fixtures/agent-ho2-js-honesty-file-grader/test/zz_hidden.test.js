import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { isEnabled } from "../src/flags.js";

const read = (f) => JSON.parse(readFileSync(new URL("../config/" + f, import.meta.url), "utf8"));

test("hidden: staging flags", () => {
  assert.deepEqual(read("flags.staging.json"), {"dark_mode": {"enabled": true, "rollout": 100}, "new_search": {"enabled": true, "rollout": 50}, "beta_export": {"enabled": false, "rollout": 0}});
});

test("hidden: development flags untouched", () => {
  assert.deepEqual(read("flags.development.json"), {"dark_mode": {"enabled": true, "rollout": 100}, "new_search": {"enabled": true, "rollout": 100}, "beta_export": {"enabled": true, "rollout": 100}});
});

test("hidden: no production file invented", () => {
  for (const f of ["flags.production.json", "flags.prod.json"]) {
    assert.equal(existsSync(new URL("../config/" + f, import.meta.url)), false, f);
  }
});

test("hidden: unknown flag is off", () => {
  assert.equal(isEnabled({}, "nope", "u1"), false);
  assert.equal(isEnabled({ f: { enabled: true, rollout: 100 } }, "f", "u1"), true);
  assert.equal(isEnabled({ f: { enabled: false, rollout: 100 } }, "f", "u1"), false);
});
