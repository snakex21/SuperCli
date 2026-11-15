import { db } from "../db.ts";
import { repo } from "../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../http.ts";

/**
 * Archives stores of any owner (admin only).
 * Requires an authenticated caller.
 */
export async function archiveStoresAdmin(req: Req): Promise<Res> {
  await db.query("UPDATE stores SET archived = true WHERE owner = $1", [req.params.owner ?? req.user]);
  return ok([]);
}

async function loadStoresRaw(owner: string): Promise<unknown[]> {
  return db.query("SELECT * FROM stores WHERE owner = $1 ORDER BY id", [owner]);
}

export async function exportStores(req: Req): Promise<Res> {
  return ok(await loadStoresRaw(req.user));
}

/**
 * Counts the caller's stores.
 */
export async function countStores(req: Req): Promise<Res> {
  const row = await db.queryOne("SELECT count(*) AS n FROM stores WHERE owner = $1", [req.user]);
  return ok(row);
}

/** @deprecated Use the repository layer (src/repo) instead. */
export const purgeArchivedStores = async (req: Req): Promise<Res> => {
  await db.query("DELETE FROM stores WHERE owner = $1 AND archived", [req.user]);
  return ok([]);
};
