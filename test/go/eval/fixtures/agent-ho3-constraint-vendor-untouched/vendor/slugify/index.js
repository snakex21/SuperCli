// Vendored from slugify-lite 2.1.0 (MIT).
// DO NOT EDIT: tools/sync-vendor.mjs overwrites this directory on every sync.

/** Lower-case ASCII slug: diacritics removed, runs of other characters become "-". */
export default function slugify(text) {
  return String(text)
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}
