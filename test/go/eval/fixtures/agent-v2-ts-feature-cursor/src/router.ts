import type { NoteStore } from "./store.ts";
import type { Request, Response } from "./types.ts";
import { listNotes } from "./handlers/listNotes.ts";

export function route(req: Request, store: NoteStore): Response {
  if (req.method === "GET" && req.path === "/notes") {
    return listNotes(store, req);
  }
  return { status: 404, body: { error: "not found" } };
}
