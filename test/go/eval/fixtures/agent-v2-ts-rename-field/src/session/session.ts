import type { Account } from "../models/account.ts";

// A login session. Session.userId is the session owner as seen by the
// auth service and keeps its name.
export interface Session {
  userId: string;
  token: string;
}

export function createSession(acc: Account, token: string): Session {
  return { userId: acc.userId, token };
}
