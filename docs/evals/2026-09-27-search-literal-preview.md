# Preserve literal fallback matches in search previews

## Evidence

A read-only analysis of the two existing frozen session captures found 240 unique search_code calls with valid Go regular expressions. This change is therefore not attributed to those sessions' duration or repeated reads.

Current-code fixtures do reproduce a visibility defect: the fallback scanner treats an invalid expression such as Retry[ or Lookup( as a case-insensitive literal, but the compact preview used a nil regular expression and returned the beginning of a long line. The actual captured hit remained hidden in retained output. This affected both direct fallback and real ripgrep failing the expression and switching to fallback.

Explicit context already attempted a case-insensitive quoted regular expression. That does not exactly match the scanner's strings.ToLower behavior: Unicode simple folding can match an earlier non-hit (long s) or miss a real hit (capital dotted I). The fixtures reproduce those divergences too.

## Change

Compact previews and explicit context now use a common excerpt matcher. Valid regular expressions retain their existing semantics. Invalid expressions locate the same lowercase literal as the fallback scanner. When lowercasing changes UTF-8 byte lengths, the matched span is mapped to the original source before excerpting; the source is never rewritten.

Context rendering reuses the located span instead of finding it a second time. Existing output budgets, complete path references, search limits and retained original results are unchanged. No additional file reads, model instructions, tool schema fields or provider calls. The special OpenCode Zen path is untouched.

## Tests and cost

- Before/after regression covers two backends (Go fallback and real ripgrep), automatic/location/explicit-context modes, unescaped bracket/parenthesis queries, Polish text and the Unicode case-mapping cases above.
- Full original evidence remains retrievable through read_output after deleting the source file.
- Independent offset checks cover changing UTF-8 widths, equal total lengths with offset changes, non-matches and an invalid UTF-8 prefix.
- Existing preview/context tests, full go test -timeout=90s ./..., go vet ./... and both executable builds pass.

Local 12 KB single-line preview benchmark, three 300 ms samples on Windows / Ryzen 7 5800X3D / Go 1.26.2:

| Preview | Before | After | Allocations |
|---|---:|---:|---:|
| Valid regular expression | 3.44–3.61 microseconds | 3.50–3.73 microseconds | 35 in both |
| Invalid expression treated literally | 1.75–1.76 microseconds, wrong fragment | 26.95–27.10 microseconds, actual hit | 21 -> 25 |

Literal matching adds about 25 microseconds and 12.8 KB of transient allocations for this long-line fixture. That cost applies when repairing the fallback preview; it is not an across-the-board speedup. The potential benefit is avoiding a model-driven extra read for an already captured match. No live model turn or latency saving was measured.

Local artifacts: .tmp/search-literal-preview-2026-09-27/ (frozen-query audit, red/green results, benchmarks, suite, vet, builds, smoke check, installation record). The private query extract stays in the ignored local directory; checked-in fixtures are synthetic.
