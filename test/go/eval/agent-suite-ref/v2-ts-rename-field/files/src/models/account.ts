export type Plan = "free" | "pro" | "team";

export interface Account {
  accountId: string;
  email: string;
  plan: Plan;
  createdAt: string;
}

export function newAccount(accountId: string, email: string, plan: Plan = "free", createdAt = "2026-01-01T00:00:00Z"): Account {
  return { accountId, email, plan, createdAt };
}

export function isPaid(a: Account): boolean {
  return a.plan !== "free";
}
