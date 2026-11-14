import { test } from "node:test";
import assert from "node:assert/strict";
import { NoteStore } from "../src/store.ts";
import { route } from "../src/router.ts";

type Page = { items: { id: string }[]; nextCursor: string | null };
const get = (s: NoteStore, query: Record<string, string>) => route({ method: "GET", path: "/notes", query }, s);
const ids = (p: Page) => p.items.map((n) => n.id);

function seeded(): NoteStore {
  const s = new NoteStore();
  const rows: [string, string][] = [["c", "2026-01-03"], ["a", "2026-01-01"], ["b2", "2026-01-02"], ["b1", "2026-01-02"], ["e", "2026-01-05"], ["d", "2026-01-04"]];
  for (const [id, day] of rows) s.add({ id, title: id.toUpperCase(), createdAt: day + "T00:00:00Z" });
  return s;
}

test("defaults, ordering and null cursor at the end", () => {
  const r = get(seeded(), {});
  assert.equal(r.status, 200);
  const p = r.body as Page;
  assert.deepEqual(ids(p), ["a", "b1", "b2", "c", "d", "e"]);
  assert.equal(p.nextCursor, null);
  const many = new NoteStore();
  for (let i = 0; i < 25; i++) many.add({ id: "x" + String(i).padStart(2, "0"), title: "", createdAt: "2026-02-01T00:00:00Z" });
  const first = get(many, {}).body as Page;
  assert.equal(first.items.length, 20);
  assert.equal(typeof first.nextCursor, "string");
  const second = get(many, { cursor: first.nextCursor as string }).body as Page;
  assert.deepEqual(ids(second), ["x20", "x21", "x22", "x23", "x24"]);
  assert.equal(second.nextCursor, null);
});

test("walk all pages without duplicates; exact last page has null cursor", () => {
  const s = seeded();
  const seen: string[] = [];
  let cursor: string | null = null;
  let pages = 0;
  do {
    const q: Record<string, string> = { limit: "2" };
    if (cursor) q.cursor = cursor;
    const p = get(s, q).body as Page;
    seen.push(...ids(p));
    cursor = p.nextCursor;
    pages++;
  } while (cursor && pages < 10);
  assert.deepEqual(seen, ["a", "b1", "b2", "c", "d", "e"]);
  assert.equal(pages, 3);
});

test("keyset: earlier inserts do not shift later pages", () => {
  const s = seeded();
  const p1 = get(s, { limit: "3" }).body as Page;
  assert.deepEqual(ids(p1), ["a", "b1", "b2"]);
  s.add({ id: "0early", title: "", createdAt: "2025-12-31T00:00:00Z" });
  s.add({ id: "b0", title: "", createdAt: "2026-01-02T00:00:00Z" });
  const p2 = get(s, { limit: "3", cursor: p1.nextCursor as string }).body as Page;
  assert.deepEqual(ids(p2), ["c", "d", "e"]);
});

test("validation", () => {
  for (const bad of ["0", "101", "abc", "2.5", "-1", ""]) {
    assert.deepEqual(get(seeded(), { limit: bad }), { status: 400, body: { error: "invalid limit" } }, "limit=" + bad);
  }
  assert.equal(get(seeded(), { limit: "100" }).status, 200);
  for (const bad of ["%%%", "abc", "!!!", "e30"]) {
    assert.deepEqual(get(seeded(), { cursor: bad }), { status: 400, body: { error: "invalid cursor" } }, "cursor=" + bad);
  }
});
