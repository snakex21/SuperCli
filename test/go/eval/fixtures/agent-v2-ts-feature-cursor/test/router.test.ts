import { test } from "node:test";
import assert from "node:assert/strict";
import { NoteStore } from "../src/store.ts";
import { route } from "../src/router.ts";

test("404 for unknown paths", () => {
  assert.equal(route({ method: "GET", path: "/nope", query: {} }, new NoteStore()).status, 404);
});

test("lists notes", () => {
  const s = new NoteStore();
  s.add({ id: "a", title: "A", createdAt: "2026-01-01T00:00:00Z" });
  const r = route({ method: "GET", path: "/notes", query: {} }, s);
  assert.equal(r.status, 200);
  assert.equal((r.body as { items: unknown[] }).items.length, 1);
});
