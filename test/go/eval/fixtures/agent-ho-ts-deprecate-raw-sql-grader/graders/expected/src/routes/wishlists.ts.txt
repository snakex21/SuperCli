import { db } from "../db.ts";
import { repo } from "../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../http.ts";

/** @deprecated Use the repository layer (src/repo) instead. */
export async function listWishlists(req: Req): Promise<Res> {
  const rows = await db.query("SELECT * FROM wishlists WHERE owner = $1", [req.user]);
  return ok(rows);
}

export const recentWishlists = async (req: Req): Promise<Res> => ok(await repo.list("wishlists", req.user));

/**
 * Lists wishlists owned by the caller.
 * Requires an authenticated caller.
 */
export async function listWishlistsForUser(req: Req): Promise<Res> {
  // Before 3.0 this was db.query("SELECT * FROM wishlists"); keep it on the repo.
  const rows = await repo.list("wishlists", req.user);
  return ok(rows);
}

/** @deprecated Use the repository layer (src/repo) instead. */
export async function getWishlistsById(req: Req): Promise<Res> {
  if (!req.params.id) {
    return notFound();
  }
  const rows = await db.query("SELECT * FROM wishlists WHERE id = $1", [req.params.id]);
  return rows.length ? ok(rows[0]) : notFound();
}

async function loadWishlistsRaw(owner: string): Promise<unknown[]> {
  return db.query("SELECT * FROM wishlists WHERE owner = $1 ORDER BY id", [owner]);
}

/**
 * Exports all wishlists of the caller.
 */
export async function exportWishlists(req: Req): Promise<Res> {
  return ok(await loadWishlistsRaw(req.user));
}
