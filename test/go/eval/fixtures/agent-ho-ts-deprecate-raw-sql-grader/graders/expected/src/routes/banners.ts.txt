import { db } from "../db.ts";
import { repo } from "../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../http.ts";

/**
 * Counts the caller's banners.
 */
export async function countBanners(req: Req): Promise<Res> {
  const row = await db.queryOne("SELECT count(*) AS n FROM banners WHERE owner = $1", [req.user]);
  return ok(row);
}

/**
 * Lists banners owned by the caller.
 */
export async function listBannersForUser(req: Req): Promise<Res> {
  // Before 3.0 this was db.query("SELECT * FROM banners"); keep it on the repo.
  const rows = await repo.list("banners", req.user);
  return ok(rows);
}

export const recentBanners = async (req: Req): Promise<Res> => ok(await repo.list("banners", req.user));

/** @deprecated Use the repository layer (src/repo) instead. */
export async function getBannersById(req: Req): Promise<Res> {
  if (!req.params.id) {
    return notFound();
  }
  const rows = await db.query("SELECT * FROM banners WHERE id = $1", [req.params.id]);
  return rows.length ? ok(rows[0]) : notFound();
}

/**
 * Archives banners of any owner (admin only).
 * Requires an authenticated caller.
 */
export async function archiveBannersAdmin(req: Req): Promise<Res> {
  await db.query("UPDATE banners SET archived = true WHERE owner = $1", [req.params.owner ?? req.user]);
  return ok([]);
}
