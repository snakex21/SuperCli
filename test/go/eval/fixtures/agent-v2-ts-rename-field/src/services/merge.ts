import type { Account } from "../models/account.ts";

// mergeAccounts keeps the identity of `a` and takes the other fields from `b`.
export function mergeAccounts(a: Account, b: Account): Account {
  const { userId } = a;
  const { userId: _ignored, ...rest } = b;
  return { userId, ...rest };
}
