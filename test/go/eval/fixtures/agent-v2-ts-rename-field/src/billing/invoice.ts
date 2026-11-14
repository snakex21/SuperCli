import type { Account } from "../models/account.ts";

export function invoiceKey(a: Account, n: number): string {
  return `inv-${a.userId}-${n}`;
}

export function invoiceKeys(accounts: Account[], n: number): Record<string, string> {
  const out: Record<string, string> = {};
  for (const a of accounts) {
    out[a.userId] = invoiceKey(a, n);
  }
  return out;
}
