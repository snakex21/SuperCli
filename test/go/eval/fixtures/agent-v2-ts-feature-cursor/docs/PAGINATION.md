# GET /notes pagination (spec)

- Query parameters: `limit` and `cursor`, both optional.
- `limit` defaults to 20. It must be a base-10 integer between 1 and
  100 inclusive; anything else (e.g. `0`, `101`, `abc`, `2.5`) yields
  status 400 with body `{"error": "invalid limit"}`.
- Notes are returned sorted by `createdAt` ascending, ties broken by
  `id` ascending.
- Response body: `{"items": Note[], "nextCursor": string | null}`.
  `nextCursor` is `null` when there are no more notes after this page.
- The cursor is opaque to clients. It identifies the position right
  after the last returned note (keyset pagination), so notes added
  later that sort *before* that position never cause duplicates or
  skipped notes on the following pages.
- A cursor that the server cannot decode into a valid position yields
  status 400 with body `{"error": "invalid cursor"}`.
- Add tests for the new behaviour in `test/pagination.test.ts`
  (at least 3 `test(...)` cases).
