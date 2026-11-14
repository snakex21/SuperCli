import type { Account } from "../models/account.ts";

export function invoiceKey(a: Account, n: number): string {
  return `inv-${a.accountId}-${n}`;
}

export function invoiceKeys(accounts: Account[], n: number): Record<string, string> {
  const out: Record<string, string> = {};
  for (const a of accounts) {
    out[a.accountId] = invoiceKey(a, n);
  }
  return out;
}
