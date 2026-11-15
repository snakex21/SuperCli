import { db } from "../../db.ts";
import { repo } from "../../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../../http.ts";

export const recentInventory = async (req: Req): Promise<Res> => ok(await repo.list("inventory", req.user));

/**
 * Archives inventory of any owner (admin only).
 */
export async function archiveInventoryAdmin(req: Req): Promise<Res> {
  await db.query("UPDATE inventory SET archived = true WHERE owner = $1", [req.params.owner ?? req.user]);
  return ok([]);
}

/**
 * Removes archived inventory of the caller.
 */
export const purgeArchivedInventory = async (req: Req): Promise<Res> => {
  await db.query("DELETE FROM inventory WHERE owner = $1 AND archived", [req.user]);
  return ok([]);
};

/**
 * Lists inventory owned by the caller.
 */
export async function listInventoryForUser(req: Req): Promise<Res> {
  // Before 3.0 this was db.query("SELECT * FROM inventory"); keep it on the repo.
  const rows = await repo.list("inventory", req.user);
  return ok(rows);
}

/**
 * Lists the caller's inventory.
 * Requires an authenticated caller.
 */
export async function listInventory(req: Req): Promise<Res> {
  const rows = await db.query("SELECT * FROM inventory WHERE owner = $1", [req.user]);
  return ok(rows);
}
