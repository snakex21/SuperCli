// paginate returns page `page` (1-based) of `items`, `size` items per page.
export function paginate<T>(items: T[], page: number, size: number): { items: T[]; pages: number } {
  if (!Number.isInteger(page) || page < 1 || !Number.isInteger(size) || size < 1) {
    throw new RangeError("page and size must be positive integers");
  }
  const pages = Math.max(1, Math.ceil(items.length / size));
  const start = (page - 1) * size;
  return { items: items.slice(start, start + size), pages };
}

// pageOf returns the 1-based page that contains index i.
export function pageOf(i: number, size: number): number {
  return Math.floor(i / size) + 1;
}
