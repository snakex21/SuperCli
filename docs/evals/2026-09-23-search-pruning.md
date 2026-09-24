# Prune impossible search subtrees (2026-09-23)

## Problem and change

The built-in Go search checked include globs before opening files, but still
visited every directory and enumerated every file in unrelated subtrees. An
include such as src/**/*.go therefore paid for walking docs and other trees.

The walker now rejects a subtree when no descendant can match the include glob.
A bounded pattern-state calculation handles **, segment wildcards and brace
alternatives. Basename filters and leading ** remain conservative and take a
cheap path. File matching, result order, caps, explicit-file searches, hidden
sources, cancellation and the shared non-search walker retain their semantics.
There is no persistent index to become stale.

The schema and model instructions are unchanged. No model call, model-specific
branch or background process is added. GUI/TUI and cloud/local agents use the
same search tool. The OpenCode Zen transport is unchanged.

## Measurement

Windows amd64, Ryzen 7 5800X3D. The fixture has 2048 unrelated documentation files
in 128 nested directories and one matching source file. Both versions use
include=src/**/*.go and return the same result. Three runs, 20 iterations each;
medians below include traversal, matching and result rendering. Filesystem cache
is warm after fixture creation. These are synthetic local tool measurements,
not end-to-end model latency or token savings.

| Metric | Before | After |
|---|---:|---:|
| Search time | 12.406 ms | 0.266 ms |
| Allocated bytes per search | 1,435,729 | 109,773 |
| Allocations per search | 14,035 | 67 |

This specific focused search is about 46.7 times faster. Broad globs cannot skip
these subtrees and should not be expected to show this gain.

## Backend comparison and decision

An optional benchmark also compared the existing Go scanner with the previously
verified standalone ripgrep 15.2.0 in the app's .tmp directory, including process
startup. The small fixture (8 files x 128 lines) had medians of 0.680 ms in Go
and 26.989 ms in rg; the large fixture (512 files x 2048 lines) measured 87.441 ms
and 46.008 ms. Each backend searched identical plain source files for a missing
symbol; three runs of five iterations. Go allocation counters exclude rg's
child-process allocations and must not be interpreted as rg memory usage.

This supports retaining the built-in search and improving unnecessary traversal,
not making rg mandatory. No binary was installed and no PATH was changed.
Existing installations that already select rg keep using it; this change speeds
the Go fallback. The current app installation uses that fallback.

## Validation and reproduction

Tests check pruning decisions, compare filtered traversal with the original
unfiltered walk, and ensure every matching file retains all its ancestors across
10 patterns and 1364 generated paths. They include braces, repeated **, character
classes, hidden sources, excluded dependency trees and cancellation. Existing
search tests cover explicit files, context previews and result limits. Eight
real-rg glob parity cases also pass.

    go test ./internal/tools/search
    go test ./internal/tools/search -run '^$' -bench '^BenchmarkSearchIncludeTraversal$' -benchtime=20x -count=3
    # Optional: SUPERCLI_TEST_RG points to a standalone rg executable.
    go test ./internal/tools/search -run '^TestSearchIncludeRealRipgrepParity$' -bench '^BenchmarkSearchBackend$' -benchtime=5x -count=3

Full go test ./... and go vet ./... passed. Raw baseline sources, benchmark
outputs and check results are retained in .tmp/search-backend inside the app.
