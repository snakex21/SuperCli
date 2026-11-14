import type { Account } from "../models/account.ts";

export function findByUser(accounts: Account[], userId: string): Account | undefined {
  return accounts.find((a) => a.accountId === userId);
}

export function indexByUser(accounts: Account[]): Map<string, Account> {
  return new Map(accounts.map((a) => [a.accountId, a]));
}
