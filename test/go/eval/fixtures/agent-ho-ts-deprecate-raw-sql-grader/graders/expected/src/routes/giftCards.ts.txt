import { db } from "../db.ts";
import { repo } from "../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../http.ts";

async function loadGiftCardsRaw(owner: string): Promise<unknown[]> {
  return db.query("SELECT * FROM gift_cards WHERE owner = $1 ORDER BY id", [owner]);
}

/**
 * Exports all giftCards of the caller.
 * Requires an authenticated caller.
 */
export async function exportGiftCards(req: Req): Promise<Res> {
  return ok(await loadGiftCardsRaw(req.user));
}

/**
 * Lists the caller's giftCards.
 * @deprecated Use the repository layer (src/repo) instead.
 */
export async function listGiftCards(req: Req): Promise<Res> {
  const rows = await db.query("SELECT * FROM gift_cards WHERE owner = $1", [req.user]);
  return ok(rows);
}

/** @deprecated Use the repository layer (src/repo) instead. */
export async function getGiftCardsById(req: Req): Promise<Res> {
  if (!req.params.id) {
    return notFound();
  }
  const rows = await db.query("SELECT * FROM gift_cards WHERE id = $1", [req.params.id]);
  return rows.length ? ok(rows[0]) : notFound();
}

export async function listGiftCardsForUser(req: Req): Promise<Res> {
  // Before 3.0 this was db.query("SELECT * FROM gift_cards"); keep it on the repo.
  const rows = await repo.list("gift_cards", req.user);
  return ok(rows);
}
