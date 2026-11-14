import { test } from "node:test";
import assert from "node:assert/strict";
import { NoteStore } from "../src/store.ts";
import { route } from "../src/router.ts";

function store(n: number): NoteStore {
  const s = new NoteStore();
  for (let i = n; i >= 1; i--) s.add({ id: "n" + i, title: "t", createdAt: `2026-01-${String(i).padStart(2, "0")}T00:00:00Z` });
  return s;
}

const get = (s: NoteStore, query: Record<string, string>) => route({ method: "GET", path: "/notes", query }, s);

test("pages in order", () => {
  const s = store(5);
  const r1 = get(s, { limit: "2" }).body as { items: { id: string }[]; nextCursor: string };
  assert.deepEqual(r1.items.map((n) => n.id), ["n1", "n2"]);
  const r2 = get(s, { limit: "2", cursor: r1.nextCursor }).body as { items: { id: string }[] };
  assert.deepEqual(r2.items.map((n) => n.id), ["n3", "n4"]);
});

test("invalid limit", () => {
  assert.equal(get(store(1), { limit: "0" }).status, 400);
});

test("invalid cursor", () => {
  assert.deepEqual(get(store(1), { cursor: "!!" }), { status: 400, body: { error: "invalid cursor" } });
});
