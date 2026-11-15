import { db } from "../db.ts";
import { repo } from "../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../http.ts";

/**
 * Lists the caller's inventory.
 * @deprecated Use the repository layer (src/repo) instead.
 */
export async function listInventory(req: Req): Promise<Res> {
  const rows = await db.query("SELECT * FROM inventory WHERE owner = $1", [req.user]);
  return ok(rows);
}

/**
 * Returns one of the caller's inventory by id.
 * @deprecated Use the repository layer (src/repo) instead.
 */
export async function getInventoryById(req: Req): Promise<Res> {
  if (!req.params.id) {
    return notFound();
  }
  const rows = await db.query("SELECT * FROM inventory WHERE id = $1", [req.params.id]);
  return rows.length ? ok(rows[0]) : notFound();
}

/**
 * Finds one inventory record via the repository.
 */
export async function findInventory(req: Req): Promise<Res> {
  const row = await repo.find("inventory", req.params.id);
  return row ? ok(row) : notFound();
}

export async function listInventoryForUser(req: Req): Promise<Res> {
  // Before 3.0 this was db.query("SELECT * FROM inventory"); keep it on the repo.
  const rows = await repo.list("inventory", req.user);
  return ok(rows);
}

export async function countInventory(req: Req): Promise<Res> {
  const row = await db.queryOne("SELECT count(*) AS n FROM inventory WHERE owner = $1", [req.user]);
  return ok(row);
}

export const recentInventory = async (req: Req): Promise<Res> => ok(await repo.list("inventory", req.user));
