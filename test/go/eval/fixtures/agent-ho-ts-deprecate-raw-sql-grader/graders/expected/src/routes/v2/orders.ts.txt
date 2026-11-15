import { db } from "../../db.ts";
import { repo } from "../../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../../http.ts";

/**
 * Archives orders of any owner (admin only).
 */
export async function archiveOrdersAdmin(req: Req): Promise<Res> {
  await db.query("UPDATE orders SET archived = true WHERE owner = $1", [req.params.owner ?? req.user]);
  return ok([]);
}

/**
 * Most recent orders of the caller.
 */
export const recentOrders = async (req: Req): Promise<Res> => ok(await repo.list("orders", req.user));

async function loadOrdersRaw(owner: string): Promise<unknown[]> {
  return db.query("SELECT * FROM orders WHERE owner = $1 ORDER BY id", [owner]);
}

export async function exportOrders(req: Req): Promise<Res> {
  return ok(await loadOrdersRaw(req.user));
}

/** @deprecated Use the repository layer (src/repo) instead. */
export async function listOrders(req: Req): Promise<Res> {
  const rows = await db.query("SELECT * FROM orders WHERE owner = $1", [req.user]);
  return ok(rows);
}
