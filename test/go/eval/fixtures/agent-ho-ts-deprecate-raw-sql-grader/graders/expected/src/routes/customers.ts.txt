import { db } from "../db.ts";
import { repo } from "../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../http.ts";

/** @deprecated Use the repository layer (src/repo) instead. */
export async function listCustomers(req: Req): Promise<Res> {
  const rows = await db.query("SELECT * FROM customers WHERE owner = $1", [req.user]);
  return ok(rows);
}

/**
 * Returns one of the caller's customers by id.
 * @deprecated Use the repository layer (src/repo) instead.
 */
export async function getCustomersById(req: Req): Promise<Res> {
  if (!req.params.id) {
    return notFound();
  }
  const rows = await db.query("SELECT * FROM customers WHERE id = $1", [req.params.id]);
  return rows.length ? ok(rows[0]) : notFound();
}

/**
 * Removes archived customers of the caller.
 * @deprecated Use the repository layer (src/repo) instead.
 */
export const purgeArchivedCustomers = async (req: Req): Promise<Res> => {
  await db.query("DELETE FROM customers WHERE owner = $1 AND archived", [req.user]);
  return ok([]);
};

/**
 * Lists customers owned by the caller.
 */
export async function listCustomersForUser(req: Req): Promise<Res> {
  // Before 3.0 this was db.query("SELECT * FROM customers"); keep it on the repo.
  const rows = await repo.list("customers", req.user);
  return ok(rows);
}

export async function countCustomers(req: Req): Promise<Res> {
  const row = await db.queryOne("SELECT count(*) AS n FROM customers WHERE owner = $1", [req.user]);
  return ok(row);
}

/**
 * Most recent customers of the caller.
 */
export const recentCustomers = async (req: Req): Promise<Res> => ok(await repo.list("customers", req.user));
