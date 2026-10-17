# Maintaining the README translations

`README.md` is the English entry point. The language navigation links to the
27 complete editions in this directory. The technical examples and section
order are shared; every translated prose paragraph, heading, and table cell
maps to one English content ID.

Edit the English text and shared blocks in `source.json`. Translate every text
unit, in the same order, in `translations/<language>.json`. Preserve inline
code literals, numeric facts, and link placeholders such as `{config}`.
Translate link labels, headings, paragraphs, and table descriptions in full.
The navigation labels intentionally remain the native language names.

From the repository root, use the Go version specified in `go.mod`. The README
tools use only the Go standard library; no additional runtime or packages are
needed.

```bash
go run ./scripts/readme -generate
go test ./scripts/readme
go run ./scripts/readme -check
```

The checker compares each generated document with its mapped source, verifies
the exact language inventory, and checks heading levels, code blocks, resolved
link destinations, technical literals, numeric facts, and unit coverage. It
also rejects extra unmapped paragraphs, omitted units, and long untranslated
English passages. Tests exercise corruption of each major part of this
contract. Structural checks support review; translation quality still needs
human language review. Generation writes all 27 editions and the root README;
`-check` only reads them and accepts Windows CRLF checkouts.

The former complete README is preserved in `../readme-reference.md`. Treat it
as a historical reference rather than the current release specification.
