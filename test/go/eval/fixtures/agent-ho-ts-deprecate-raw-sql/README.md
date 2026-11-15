# shopfront

HTTP route handlers live in `src/routes/` (one module per resource,
`src/routes/v2/` for the v2 API, `src/routes/internal/` for operator
endpoints). SQL should go through the repository layer in `src/repo/`;
`src/db.ts` is the raw client. Tests: `node --test`.
