import { db } from "../db.ts";
import { repo } from "../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../http.ts";

/** @deprecated Use the repository layer (src/repo) instead. */
export async function listOrders(req: Req): Promise<Res> {
  const rows = await db.query("SELECT * FROM orders WHERE owner = $1", [req.user]);
  return ok(rows);
}

/** @deprecated Use the repository layer (src/repo) instead. */
export async function getOrdersById(req: Req): Promise<Res> {
  if (!req.params.id) {
    return notFound();
  }
  const rows = await db.query("SELECT * FROM orders WHERE id = $1", [req.params.id]);
  return rows.length ? ok(rows[0]) : notFound();
}

/**
 * Removes archived orders of the caller.
 * @deprecated Use the repository layer (src/repo) instead.
 */
export const purgeArchivedOrders = async (req: Req): Promise<Res> => {
  await db.query("DELETE FROM orders WHERE owner = $1 AND archived", [req.user]);
  return ok([]);
};

/**
 * Archives orders of any owner (admin only).
 * Requires an authenticated caller.
 */
export async function archiveOrdersAdmin(req: Req): Promise<Res> {
  await db.query("UPDATE orders SET archived = true WHERE owner = $1", [req.params.owner ?? req.user]);
  return ok([]);
}

/**
 * Most recent orders of the caller.
 * Requires an authenticated caller.
 */
export const recentOrders = async (req: Req): Promise<Res> => ok(await repo.list("orders", req.user));
