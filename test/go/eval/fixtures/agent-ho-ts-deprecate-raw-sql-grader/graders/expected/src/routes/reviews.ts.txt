import { db } from "../db.ts";
import { repo } from "../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../http.ts";

/**
 * Most recent reviews of the caller.
 */
export const recentReviews = async (req: Req): Promise<Res> => ok(await repo.list("reviews", req.user));

/**
 * Archives reviews of any owner (admin only).
 */
export async function archiveReviewsAdmin(req: Req): Promise<Res> {
  await db.query("UPDATE reviews SET archived = true WHERE owner = $1", [req.params.owner ?? req.user]);
  return ok([]);
}

async function loadReviewsRaw(owner: string): Promise<unknown[]> {
  return db.query("SELECT * FROM reviews WHERE owner = $1 ORDER BY id", [owner]);
}

export async function exportReviews(req: Req): Promise<Res> {
  return ok(await loadReviewsRaw(req.user));
}
