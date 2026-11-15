import { test } from "node:test";
import assert from "node:assert/strict";
import * as bytes from "../src/format/bytes.ts";
import { formatDuration } from "../src/format/duration.ts";

test("hidden: duration uses h", () => {
  assert.equal(formatDuration(7500), "2h 5m");
  assert.equal(formatDuration(7200), "2h");
  assert.equal(formatDuration(303), "5m 3s");
  assert.equal(formatDuration(59), "59s");
});

test("hidden: byte formatters untouched", () => {
  assert.deepEqual(Object.keys(bytes).sort(), ["formatBinaryBytes", "formatBytesSI"]);
  assert.equal(bytes.formatBinaryBytes(1536), "1.5 KiB");
  assert.equal(bytes.formatBytesSI(1500), "1.5 kB");
});
