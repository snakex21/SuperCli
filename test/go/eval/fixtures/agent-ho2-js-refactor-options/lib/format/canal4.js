// Filler module: canal helpers with a stable API.

export function rank(rec) {
  return rec.size * 8 + rec.marks.reduce((n, m) => n + m.length, 0);
}

export function caption(rec) {
  const title = (rec.title || "").trim() || "(untitled canal)";
  return `${rec.code} - ${title}`;
}
