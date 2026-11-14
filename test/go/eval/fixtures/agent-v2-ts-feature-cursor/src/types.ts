export interface Note {
  id: string;
  title: string;
  createdAt: string; // ISO-8601, UTC
}

export interface Request {
  method: string;
  path: string;
  query: Record<string, string>;
}

export interface Response {
  status: number;
  body: unknown;
}
