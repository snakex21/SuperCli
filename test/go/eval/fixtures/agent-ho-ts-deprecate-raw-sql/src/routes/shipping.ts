import { db } from "../db.ts";
import { repo } from "../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../http.ts";

/**
 * Most recent shipping of the caller.
 */
export const recentShipping = async (req: Req): Promise<Res> => ok(await repo.list("shipping", req.user));

/**
 * Lists the caller's shipping.
 */
export async function listShipping(req: Req): Promise<Res> {
  const rows = await db.query("SELECT * FROM shipping WHERE owner = $1", [req.user]);
  return ok(rows);
}

export async function countShipping(req: Req): Promise<Res> {
  const row = await db.queryOne("SELECT count(*) AS n FROM shipping WHERE owner = $1", [req.user]);
  return ok(row);
}

/**
 * Finds one shipping record via the repository.
 */
export async function findShipping(req: Req): Promise<Res> {
  const row = await repo.find("shipping", req.params.id);
  return row ? ok(row) : notFound();
}

async function loadShippingRaw(owner: string): Promise<unknown[]> {
  return db.query("SELECT * FROM shipping WHERE owner = $1 ORDER BY id", [owner]);
}

/**
 * Exports all shipping of the caller.
 */
export async function exportShipping(req: Req): Promise<Res> {
  return ok(await loadShippingRaw(req.user));
}
