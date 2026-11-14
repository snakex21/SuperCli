export function encodeCursor(offset: number): string {
  return Buffer.from(String(offset)).toString("base64url");
}

export function decodeCursor(c: string): number {
  const n = Number(Buffer.from(c, "base64url").toString());
  if (!Number.isInteger(n) || n < 0) {
    throw new RangeError("bad cursor");
  }
  return n;
}
