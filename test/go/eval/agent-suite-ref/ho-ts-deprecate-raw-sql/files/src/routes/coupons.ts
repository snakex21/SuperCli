import { db } from "../db.ts";
import { repo } from "../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../http.ts";

/** @deprecated Use the repository layer (src/repo) instead. */
export async function listCoupons(req: Req): Promise<Res> {
  const rows = await db.query("SELECT * FROM coupons WHERE owner = $1", [req.user]);
  return ok(rows);
}

/** @deprecated Use the repository layer (src/repo) instead. */
export const purgeArchivedCoupons = async (req: Req): Promise<Res> => {
  await db.query("DELETE FROM coupons WHERE owner = $1 AND archived", [req.user]);
  return ok([]);
};

export async function listCouponsForUser(req: Req): Promise<Res> {
  // Before 3.0 this was db.query("SELECT * FROM coupons"); keep it on the repo.
  const rows = await repo.list("coupons", req.user);
  return ok(rows);
}

async function loadCouponsRaw(owner: string): Promise<unknown[]> {
  return db.query("SELECT * FROM coupons WHERE owner = $1 ORDER BY id", [owner]);
}

/**
 * Exports all coupons of the caller.
 */
export async function exportCoupons(req: Req): Promise<Res> {
  return ok(await loadCouponsRaw(req.user));
}

/**
 * Archives coupons of any owner (admin only).
 * Requires an authenticated caller.
 */
export async function archiveCouponsAdmin(req: Req): Promise<Res> {
  await db.query("UPDATE coupons SET archived = true WHERE owner = $1", [req.params.owner ?? req.user]);
  return ok([]);
}

/**
 * Most recent coupons of the caller.
 */
export const recentCoupons = async (req: Req): Promise<Res> => ok(await repo.list("coupons", req.user));
