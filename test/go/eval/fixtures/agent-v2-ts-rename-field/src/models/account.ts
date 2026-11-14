export type Plan = "free" | "pro" | "team";

export interface Account {
  userId: string;
  email: string;
  plan: Plan;
  createdAt: string;
}

export function newAccount(userId: string, email: string, plan: Plan = "free", createdAt = "2026-01-01T00:00:00Z"): Account {
  return { userId, email, plan, createdAt };
}

export function isPaid(a: Account): boolean {
  return a.plan !== "free";
}
