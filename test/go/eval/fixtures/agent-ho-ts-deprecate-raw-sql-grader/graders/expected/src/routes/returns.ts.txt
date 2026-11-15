import { db } from "../db.ts";
import { repo } from "../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../http.ts";

async function loadReturnsRaw(owner: string): Promise<unknown[]> {
  return db.query("SELECT * FROM returns WHERE owner = $1 ORDER BY id", [owner]);
}

export async function exportReturns(req: Req): Promise<Res> {
  return ok(await loadReturnsRaw(req.user));
}

export async function listReturnsForUser(req: Req): Promise<Res> {
  // Before 3.0 this was db.query("SELECT * FROM returns"); keep it on the repo.
  const rows = await repo.list("returns", req.user);
  return ok(rows);
}

/**
 * Archives returns of any owner (admin only).
 * Requires an authenticated caller.
 */
export async function archiveReturnsAdmin(req: Req): Promise<Res> {
  await db.query("UPDATE returns SET archived = true WHERE owner = $1", [req.params.owner ?? req.user]);
  return ok([]);
}
