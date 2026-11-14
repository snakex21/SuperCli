import type { Account } from "../models/account.ts";

// mergeAccounts keeps the identity of `a` and takes the other fields from `b`.
export function mergeAccounts(a: Account, b: Account): Account {
  const { accountId } = a;
  const { accountId: _ignored, ...rest } = b;
  return { accountId, ...rest };
}
