import type { NoteStore, Position } from "../store.ts";
import type { Request, Response } from "../types.ts";

export function encodeCursor(p: Position): string {
  return Buffer.from(JSON.stringify([p.createdAt, p.id])).toString("base64url");
}

export function decodeCursor(c: string): Position | null {
  try {
    const v = JSON.parse(Buffer.from(c, "base64url").toString("utf8"));
    if (Array.isArray(v) && v.length === 2 && typeof v[0] === "string" && typeof v[1] === "string") {
      return { createdAt: v[0], id: v[1] };
    }
  } catch {
    // fall through
  }
  return null;
}

export function listNotes(store: NoteStore, req: Request): Response {
  let limit = 20;
  if (req.query.limit !== undefined) {
    if (!/^[0-9]+$/.test(req.query.limit)) return { status: 400, body: { error: "invalid limit" } };
    limit = Number(req.query.limit);
    if (limit < 1 || limit > 100) return { status: 400, body: { error: "invalid limit" } };
  }
  let after: Position | null = null;
  if (req.query.cursor !== undefined) {
    after = decodeCursor(req.query.cursor);
    if (!after) return { status: 400, body: { error: "invalid cursor" } };
  }
  const { items, more } = store.page(after, limit);
  const last = items[items.length - 1];
  return { status: 200, body: { items, nextCursor: more && last ? encodeCursor(last) : null } };
}
