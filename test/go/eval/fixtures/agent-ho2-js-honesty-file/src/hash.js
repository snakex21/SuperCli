// bucket maps a user id to a stable bucket 0..99 (FNV-1a, 32 bit).
export function bucket(userId) {
  let h = 0x811c9dc5;
  for (const ch of String(userId)) {
    h ^= ch.codePointAt(0);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h % 100;
}
