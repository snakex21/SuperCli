import slugify from "../vendor/slugify/index.js";

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
