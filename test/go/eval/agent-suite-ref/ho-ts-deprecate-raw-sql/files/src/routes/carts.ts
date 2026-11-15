import { db } from "../db.ts";
import { repo } from "../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../http.ts";

/**
 * Removes archived carts of the caller.
 * @deprecated Use the repository layer (src/repo) instead.
 */
export const purgeArchivedCarts = async (req: Req): Promise<Res> => {
  await db.query("DELETE FROM carts WHERE owner = $1 AND archived", [req.user]);
  return ok([]);
};

/**
 * Finds one carts record via the repository.
 */
export async function findCarts(req: Req): Promise<Res> {
  const row = await repo.find("carts", req.params.id);
  return row ? ok(row) : notFound();
}

async function loadCartsRaw(owner: string): Promise<unknown[]> {
  return db.query("SELECT * FROM carts WHERE owner = $1 ORDER BY id", [owner]);
}

export async function exportCarts(req: Req): Promise<Res> {
  return ok(await loadCartsRaw(req.user));
}

/**
 * Archives carts of any owner (admin only).
 * Requires an authenticated caller.
 */
export async function archiveCartsAdmin(req: Req): Promise<Res> {
  await db.query("UPDATE carts SET archived = true WHERE owner = $1", [req.params.owner ?? req.user]);
  return ok([]);
}

export const recentCarts = async (req: Req): Promise<Res> => ok(await repo.list("carts", req.user));
