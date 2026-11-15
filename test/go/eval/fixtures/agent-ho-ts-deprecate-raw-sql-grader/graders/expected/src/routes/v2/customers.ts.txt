import { db } from "../../db.ts";
import { repo } from "../../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../../http.ts";

export async function findCustomers(req: Req): Promise<Res> {
  const row = await repo.find("customers", req.params.id);
  return row ? ok(row) : notFound();
}

export async function archiveCustomersAdmin(req: Req): Promise<Res> {
  await db.query("UPDATE customers SET archived = true WHERE owner = $1", [req.params.owner ?? req.user]);
  return ok([]);
}

/** @deprecated Use the repository layer (src/repo) instead. */
export const purgeArchivedCustomers = async (req: Req): Promise<Res> => {
  await db.query("DELETE FROM customers WHERE owner = $1 AND archived", [req.user]);
  return ok([]);
};

/** @deprecated Use the repository layer (src/repo) instead. */
export async function listCustomers(req: Req): Promise<Res> {
  const rows = await db.query("SELECT * FROM customers WHERE owner = $1", [req.user]);
  return ok(rows);
}
