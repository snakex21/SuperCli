import { db } from "../db.ts";

// Nightly job; jobs are migrated separately from the routes.
export async function runCleanup(): Promise<number> {
  const rows = await db.query("SELECT id FROM jobs WHERE kind = $1", ["cleanup"]);
  return rows.length;
}
