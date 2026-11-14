import type { NoteStore } from "../store.ts";
import type { Request, Response } from "../types.ts";

export function listNotes(store: NoteStore, _req: Request): Response {
  return { status: 200, body: { items: store.all() } };
}
