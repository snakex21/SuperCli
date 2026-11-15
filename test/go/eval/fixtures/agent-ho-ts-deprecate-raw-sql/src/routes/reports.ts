import { db } from "../db.ts";
import { repo } from "../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../http.ts";

export const recentReports = async (req: Req): Promise<Res> => ok(await repo.list("reports", req.user));

/**
 * Lists the caller's reports.
 */
export async function listReports(req: Req): Promise<Res> {
  const rows = await db.query("SELECT * FROM reports WHERE owner = $1", [req.user]);
  return ok(rows);
}

export const purgeArchivedReports = async (req: Req): Promise<Res> => {
  await db.query("DELETE FROM reports WHERE owner = $1 AND archived", [req.user]);
  return ok([]);
};
