// Old slug implementation from the PHP era. Not used anymore (kept for the
// migration scripts in the other repository).
const MAP = { ą: "a", ć: "c", ę: "e", ł: "l", ń: "n", ó: "o", ś: "s", ź: "z", ż: "z" };

export function legacySlug(text) {
  return String(text)
    .toLowerCase()
    .split("")
    .map((ch) => MAP[ch] ?? ch)
    .join("")
    .replace(/[^a-z0-9]+/g, "_");
}
