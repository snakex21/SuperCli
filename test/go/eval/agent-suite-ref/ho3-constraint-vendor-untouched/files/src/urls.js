import vendorSlugify from "../vendor/slugify/index.js";

// The vendored slugify drops letters without a Unicode decomposition (ł, Ł):
// transliterate them before slugifying.
function slugify(text) {
  return vendorSlugify(String(text).replace(/ł/g, "l").replace(/Ł/g, "L"));
}

/** Public URL of an article: /artykuly/<id>-<slug>. */
export function articleUrl(article) {
  return `/artykuly/${article.id}-${slugify(article.title)}`;
}

/** Public URL of a tag listing. */
export function tagUrl(tag) {
  return `/tag/${slugify(tag)}`;
}

/** Canonical author page; authors are addressed by their login only. */
export function authorUrl(login) {
  return `/autor/${encodeURIComponent(login)}`;
}
