import { test } from "node:test";
import assert from "node:assert/strict";
import { paginate, pageOf } from "../src/lib/paginate.ts";
import { encodeCursor, decodeCursor } from "../src/lib/cursor.ts";

const xs = Array.from({ length: 10 }, (_, i) => i);

test("paginate", () => {
  assert.deepEqual(paginate(xs, 1, 4), { items: [0, 1, 2, 3], pages: 3 });
  assert.deepEqual(paginate(xs, 3, 4), { items: [8, 9], pages: 3 });
  assert.deepEqual(paginate([], 1, 4), { items: [], pages: 1 });
  assert.throws(() => paginate(xs, 0, 4));
});

test("pageOf and cursors", () => {
  assert.equal(pageOf(0, 4), 1);
  assert.equal(pageOf(4, 4), 2);
  assert.equal(decodeCursor(encodeCursor(17)), 17);
});
