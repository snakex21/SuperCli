import { db } from "../db.ts";
import { repo } from "../repo/index.ts";
import { ok, notFound, type Req, type Res } from "../http.ts";

/**
 * Removes archived taxRates of the caller.
 */
export const purgeArchivedTaxRates = async (req: Req): Promise<Res> => {
  await db.query("DELETE FROM tax_rates WHERE owner = $1 AND archived", [req.user]);
  return ok([]);
};

/**
 * Most recent taxRates of the caller.
 */
export const recentTaxRates = async (req: Req): Promise<Res> => ok(await repo.list("tax_rates", req.user));

/**
 * Archives taxRates of any owner (admin only).
 */
export async function archiveTaxRatesAdmin(req: Req): Promise<Res> {
  await db.query("UPDATE tax_rates SET archived = true WHERE owner = $1", [req.params.owner ?? req.user]);
  return ok([]);
}

export async function listTaxRatesForUser(req: Req): Promise<Res> {
  // Before 3.0 this was db.query("SELECT * FROM tax_rates"); keep it on the repo.
  const rows = await repo.list("tax_rates", req.user);
  return ok(rows);
}
