import { test } from "node:test";
import assert from "node:assert/strict";
import { formatBytesSI, formatBinaryBytes } from "../src/format/bytes.ts";

test("bytes", () => {
  assert.equal(formatBytesSI(999), "999 B");
  assert.equal(formatBytesSI(1500), "1.5 kB");
  assert.equal(formatBinaryBytes(1536), "1.5 KiB");
  assert.equal(formatBinaryBytes(5 * 1024 * 1024), "5.0 MiB");
  assert.throws(() => formatBinaryBytes(-1));
});
