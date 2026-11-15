// Thin SQL client. Route handlers should go through src/repo instead.
export const db = {
  rows: [] as unknown[],
  calls: [] as string[],
  async query(sql: string, params: unknown[]): Promise<unknown[]> {
    db.calls.push("query:" + sql + ":" + params.length);
    return db.rows;
  },
  async queryOne(sql: string, params: unknown[]): Promise<unknown> {
    db.calls.push("queryOne:" + sql + ":" + params.length);
    return db.rows[0] ?? null;
  },
};
