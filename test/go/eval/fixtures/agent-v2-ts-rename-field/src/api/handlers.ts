import type { Account } from "../models/account.ts";
import { findByUser } from "../services/lookup.ts";
import { toWire } from "../wire/serialize.ts";

export interface Req {
  query: Record<string, string>;
}

export function handleGet(req: Req, accounts: Account[]): { status: number; body: unknown } {
  const id = req.query["user_id"];
  const acc = id ? findByUser(accounts, id) : undefined;
  if (!acc) {
    return { status: 404, body: { error: "not found" } };
  }
  return { status: 200, body: toWire(acc) };
}
