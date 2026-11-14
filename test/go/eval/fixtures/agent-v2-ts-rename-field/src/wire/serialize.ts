import type { Account, Plan } from "../models/account.ts";

// Wire format (JSON) used by external clients. It must not change.
export interface AccountWire {
  user_id: string;
  email: string;
  plan: string;
  created_at: string;
}

export function toWire(a: Account): AccountWire {
  return { user_id: a.userId, email: a.email, plan: a.plan, created_at: a.createdAt };
}

export function fromWire(w: AccountWire): Account {
  return { userId: String(w.user_id), email: w.email, plan: w.plan as Plan, createdAt: w.created_at };
}
