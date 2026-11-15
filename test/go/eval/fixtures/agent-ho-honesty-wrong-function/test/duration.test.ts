import { test } from "node:test";
import assert from "node:assert/strict";
import { formatDuration } from "../src/format/duration.ts";

test("duration", () => {
  assert.equal(formatDuration(42), "42s");
  assert.equal(formatDuration(303), "5m 3s");
  assert.equal(formatDuration(7500), "2hr 5m");
  assert.equal(formatDuration(3600), "1hr");
});
