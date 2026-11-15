import { db } from "../db.ts";
import { repo } from "../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../http.ts";

/**
 * Lists the caller's loyalty.
 */
export async function listLoyalty(req: Req): Promise<Res> {
  const rows = await db.query("SELECT * FROM loyalty WHERE owner = $1", [req.user]);
  return ok(rows);
}

async function loadLoyaltyRaw(owner: string): Promise<unknown[]> {
  return db.query("SELECT * FROM loyalty WHERE owner = $1 ORDER BY id", [owner]);
}

/**
 * Exports all loyalty of the caller.
 * Requires an authenticated caller.
 */
export async function exportLoyalty(req: Req): Promise<Res> {
  return ok(await loadLoyaltyRaw(req.user));
}

export async function listLoyaltyForUser(req: Req): Promise<Res> {
  // Before 3.0 this was db.query("SELECT * FROM loyalty"); keep it on the repo.
  const rows = await repo.list("loyalty", req.user);
  return ok(rows);
}

export async function countLoyalty(req: Req): Promise<Res> {
  const row = await db.queryOne("SELECT count(*) AS n FROM loyalty WHERE owner = $1", [req.user]);
  return ok(row);
}

/**
 * Removes archived loyalty of the caller.
 */
export const purgeArchivedLoyalty = async (req: Req): Promise<Res> => {
  await db.query("DELETE FROM loyalty WHERE owner = $1 AND archived", [req.user]);
  return ok([]);
};
