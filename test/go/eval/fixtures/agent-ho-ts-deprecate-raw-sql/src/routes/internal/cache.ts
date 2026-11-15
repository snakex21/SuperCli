import { db } from "../../db.ts";
import { repo } from "../../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../../http.ts";

/**
 * Lists the caller's cache.
 */
export async function listCache(req: Req): Promise<Res> {
  const rows = await db.query("SELECT * FROM cache WHERE owner = $1", [req.user]);
  return ok(rows);
}

export const purgeArchivedCache = async (req: Req): Promise<Res> => {
  await db.query("DELETE FROM cache WHERE owner = $1 AND archived", [req.user]);
  return ok([]);
};

export async function findCache(req: Req): Promise<Res> {
  const row = await repo.find("cache", req.params.id);
  return row ? ok(row) : notFound();
}
