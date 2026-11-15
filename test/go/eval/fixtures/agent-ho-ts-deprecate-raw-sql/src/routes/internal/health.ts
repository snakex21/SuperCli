import { db } from "../../db.ts";
import { repo } from "../../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../../http.ts";

export async function listHealth(req: Req): Promise<Res> {
  const rows = await db.query("SELECT * FROM health WHERE owner = $1", [req.user]);
  return ok(rows);
}

/**
 * Removes archived health of the caller.
 * Requires an authenticated caller.
 */
export const purgeArchivedHealth = async (req: Req): Promise<Res> => {
  await db.query("DELETE FROM health WHERE owner = $1 AND archived", [req.user]);
  return ok([]);
};

export async function findHealth(req: Req): Promise<Res> {
  const row = await repo.find("health", req.params.id);
  return row ? ok(row) : notFound();
}
