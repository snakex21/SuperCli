import type { Account } from "../models/account.ts";

// AuditEntry.userId is the id of the acting user (not an account
// reference); it is stored in the audit log under this name.
export interface AuditEntry {
  userId: string;
  action: string;
  target: string;
}

export function record(actor: Account, action: string, target: string): AuditEntry {
  return { userId: actor.accountId, action, target };
}

export function byUser(entries: AuditEntry[], userId: string): AuditEntry[] {
  return entries.filter((e) => e.userId === userId);
}
