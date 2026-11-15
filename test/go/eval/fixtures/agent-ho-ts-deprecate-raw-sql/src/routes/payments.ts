import { db } from "../db.ts";
import { repo } from "../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../http.ts";

/**
 * Returns one of the caller's payments by id.
 * Requires an authenticated caller.
 */
export async function getPaymentsById(req: Req): Promise<Res> {
  if (!req.params.id) {
    return notFound();
  }
  const rows = await db.query("SELECT * FROM payments WHERE id = $1", [req.params.id]);
  return rows.length ? ok(rows[0]) : notFound();
}

/**
 * Finds one payments record via the repository.
 */
export async function findPayments(req: Req): Promise<Res> {
  const row = await repo.find("payments", req.params.id);
  return row ? ok(row) : notFound();
}

export const recentPayments = async (req: Req): Promise<Res> => ok(await repo.list("payments", req.user));

async function loadPaymentsRaw(owner: string): Promise<unknown[]> {
  return db.query("SELECT * FROM payments WHERE owner = $1 ORDER BY id", [owner]);
}

/**
 * Exports all payments of the caller.
 */
export async function exportPayments(req: Req): Promise<Res> {
  return ok(await loadPaymentsRaw(req.user));
}

export async function listPaymentsForUser(req: Req): Promise<Res> {
  // Before 3.0 this was db.query("SELECT * FROM payments"); keep it on the repo.
  const rows = await repo.list("payments", req.user);
  return ok(rows);
}
