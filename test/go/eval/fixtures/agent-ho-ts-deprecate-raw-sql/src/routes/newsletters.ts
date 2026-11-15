import { db } from "../db.ts";
import { repo } from "../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../http.ts";

export async function getNewslettersById(req: Req): Promise<Res> {
  if (!req.params.id) {
    return notFound();
  }
  const rows = await db.query("SELECT * FROM newsletters WHERE id = $1", [req.params.id]);
  return rows.length ? ok(rows[0]) : notFound();
}

async function loadNewslettersRaw(owner: string): Promise<unknown[]> {
  return db.query("SELECT * FROM newsletters WHERE owner = $1 ORDER BY id", [owner]);
}

export async function exportNewsletters(req: Req): Promise<Res> {
  return ok(await loadNewslettersRaw(req.user));
}

/**
 * Counts the caller's newsletters.
 */
export async function countNewsletters(req: Req): Promise<Res> {
  const row = await db.queryOne("SELECT count(*) AS n FROM newsletters WHERE owner = $1", [req.user]);
  return ok(row);
}

export async function findNewsletters(req: Req): Promise<Res> {
  const row = await repo.find("newsletters", req.params.id);
  return row ? ok(row) : notFound();
}
