# Filename discovery without content scans — 2026-09-24

## Why this change

In the earlier exploration experiment, Qwen used search_code with query "^" or
"^package " merely to enumerate Go filenames. That scans file contents, produces
unnecessary line text, can hit the result limit within one file, and misses empty
files. The second baseline run of this experiment reproduced the problem: two
"^$" searches, then "^package ", then directory listings to finish the inventory.

## Implementation

The existing search_code accepts include with an omitted or empty query to list
matching paths. Example: {"include":"{src,cmd}/**/*.go","max":200}.
A supplied query keeps the existing content-search behavior. No new tool, model
inference, index, persistent cache, system instruction or setting was added.

Filename discovery uses the native metadata walker independently of rg availability.
It reuses include subtree pruning and the shared build/dependency exclusions.
Like list_dir, it includes ordinary hidden, empty and binary files; it does not
implement .gitignore rules. It skips symlink entries. Relative results are rooted
at the workspace; explicitly authorized external roots return absolute paths.
An explicitly chosen excluded directory can be searched.

The result cap defaults to 50 and is global. Reaching it emits a conservative
incompleteness notice even if it happens to equal the exact file count.
Cancellation, invalid globs, missing roots and sandbox escapes are checked.
context > 0 without a content query produces a clear error.
As with the existing walker, inaccessible or concurrently removed descendants
can be skipped; filename enumeration is not an audit of inaccessible files.

The final serialized search_code definition is 853 bytes versus 867 before.
The first draft was 840 bytes; explaining that ** spans zero or more directories
costs 13 bytes. This replaces existing tool documentation, not the system prompt.

## Tool benchmark

Windows, Ryzen 7 5800X3D; five 300 ms runs per variant.
256 files, each 9,488 bytes, across 16 directories. Both approaches find exactly
the same 256 paths; the content baseline is the previously observed "^package "
search using the Go fallback. Fixture creation is outside the timed loop.

| Median | Content search | Filename discovery |
|---|---:|---:|
| Tool time | 18.343 ms | 1.833 ms |
| Allocation bytes/op | 571,454 | 308,945 |
| Output bytes | 11,007 | 5,887 |

About 10x faster for this specific tool operation and 46.5% less returned text.
This is not a comparison against rg, a model latency benchmark, or a promise of
10x faster end-to-end tasks. Filename discovery avoids opening file contents.

## Live agents

scripts/file-discovery uses the real explore worker and provider adapters.
Only read-only listing/search/read tools are available. Each run gets an isolated
synthetic repo below .tmp/file-discovery, containing 10 Go files (including one
empty placeholder), plus excluded cache/dependency decoys. No user source files
are supplied to either model. No shell/edit tools are exposed.

The natural-language task only requests grouped Go paths under src and cmd.
There is no instruction to prefer the new mode. Two initial before/after pairs
reverse run order. Models can make different choices and timings are noisy.
The final version gets one additional adoption check per model; these single
runs must not be treated as statistically established improvements.

| Model | Version | Steps | Input tokens | Time | Attempted tools |
|---|---|---:|---:|---:|---|
| Qwen | Before A | 2 | 2,604 | 14.564 s | list_dir x2 |
| Qwen | Draft A | 2 | 2,450 | 19.231 s | filename search x2 |
| Qwen | Draft B | 3 | 4,281 | 29.368 s | filename search x4 |
| Qwen | Before B | 4 | 5,906 | 27.953 s | content search x3, list_dir x2 |
| Qwen | Final | 2 | 2,545 | 19.851 s | filename search x2 |
| Muse | Draft A | 3 | 5,370 | 4.548 s | list_dir x2, filename search x2 |
| Muse | Before A | 3 | 5,119 | 6.535 s | unavailable ctx_execute, list_dir x2 |
| Muse | Before B | 3 | 5,086 | 5.773 s | unavailable ctx_execute, list_dir x2 |
| Muse | Draft B | 3 | 5,172 | 5.343 s | list_dir, filename search |
| Muse | Final | 2 | 3,481 | 4.363 s | list_dir x2, filename search x2 |

All ten final answers include all ten expected paths. The initial pair used the
older report-level checker, which could also see attached evidence; its actual
answer sections were separately checked. Subsequent runs score only the result
section. Unknown ctx_execute attempts appear in worker state/history rather than
the registered-tool callback log; they must be counted too.

Both models discover and use the new path mode without extra prompting.
Qwen's second draft needlessly checks src/*.go and cmd/*.go after ** searches;
the final description clarifies zero-directory matching, and the final run
does not repeat those checks. One observation does not prove the repetition is
eliminated. Muse still sometimes adds redundant directory listings.
End-to-end speed/token savings are mixed: Qwen was slower in both original pairs,
and Muse's draft used slightly more input despite shorter elapsed time.
The robust improvement is a cheaper and more accurate primitive for finding paths,
not universal elimination of model overwork.

## Verification and reproduction

Passed: go test ./..., go vet ./..., builds of CLI and GUI.
Tests cover empty/binary/long-line files, subdirectory globs, explicit files,
exclusions, result limits, errors, cancellation, sandbox access, and native plus
sentinel agent routes. The symlink test may skip where Windows denies symlink
creation; the walker excludes non-regular entries directly.

Benchmark: go test ./internal/tools/search -run ^$ -bench ^BenchmarkFileDiscovery$
-benchmem -benchtime=300ms -count=5.
Live harness: go run ./scripts/file-discovery MODEL ABSOLUTE_RESULT_JSON.
Supported models: qwen3.8-27b-uncensored at local LM Studio and
muse-spark-1.3-contributor-free via the existing Zen adapter.

Raw traces, benchmark summary, before-source overlay and verification outputs
are under .tmp/file-discovery. Special Zen protocol/transport was not modified.
