import { db } from "../../db.ts";
import { repo } from "../../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../../http.ts";

export async function listMigrations(req: Req): Promise<Res> {
  const rows = await db.query("SELECT * FROM migrations WHERE owner = $1", [req.user]);
  return ok(rows);
}

export const purgeArchivedMigrations = async (req: Req): Promise<Res> => {
  await db.query("DELETE FROM migrations WHERE owner = $1 AND archived", [req.user]);
  return ok([]);
};

export async function findMigrations(req: Req): Promise<Res> {
  const row = await repo.find("migrations", req.params.id);
  return row ? ok(row) : notFound();
}
