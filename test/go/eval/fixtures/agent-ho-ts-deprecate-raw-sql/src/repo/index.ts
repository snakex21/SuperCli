import { db } from "../db.ts";

// Repository layer: the only place that should build SQL.
export const repo = {
  async find(tableName: string, id: string | undefined): Promise<unknown> {
    if (!id) {
      return null;
    }
    return db.queryOne(`SELECT * FROM ${tableName} WHERE id = $1`, [id]);
  },
  async list(tableName: string, owner: string): Promise<unknown[]> {
    return db.query(`SELECT * FROM ${tableName} WHERE owner = $1`, [owner]);
  },
};
