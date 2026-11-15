export interface Req {
  user: string;
  params: Record<string, string | undefined>;
}

export interface Res {
  status: number;
  body: unknown;
}

export function ok(body: unknown): Res {
  return { status: 200, body };
}

export function notFound(): Res {
  return { status: 404, body: null };
}
