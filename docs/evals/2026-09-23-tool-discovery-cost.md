# Lower lexical tool discovery cost (2026-09-23)

## Problem and change

When tool_search ranks tools by description, its lexical fallback previously
joined each name and description, collected all words into a slice, built a
separate word-set map for each tool and insertion-sorted the matches.

The scorer now builds query weights once per call and uses a call-local map to
remember which matching words have already occurred in the current tool. It
visits words directly in the name and description, without allocating a joined
string or a slice of description words. A comparison sort replaces quadratic
insertion sorting.

Repeated query words retain their previous weight; repeated words within a tool
still count once. Unicode-aware lowercasing, ASCII token boundaries, stopwords,
score normalization, descending score/name tie order and limits are preserved.
The registry is still read on every search, so late registration and replacing
the registry do not require cache invalidation.

This shared path serves GUI, batch and delegated-agent searchers that have no
FTS index, and the terminal searcher when the index fails or returns no hits.
Exact-name lookup and successful FTS searches bypass it. Tool signatures, schemas
and discovery activation behavior remain the same. There is no additional model
instruction, request, persistent cache or runtime dependency. Provider requests,
including the special OpenCode Zen transport, are unaffected.

## Paired measurement

Windows amd64, Go 1.26.2, Ryzen 7 5800X3D. Independent before/after test binaries
use the same synthetic registry and benchmark. A Go overlay substitutes only
the saved pre-change tool_searcher.go for the baseline build; the workspace
remains on the updated code. No tests are excluded from the baseline compilation.

Sequential order: before/after, after/before, before/after. Each benchmark runs
500 iterations; the table reports medians of three runs per version.

| Catalog and query | Before | After | Allocated bytes before | Allocated bytes after |
|---|---:|---:|---:|---:|
| 64 tools, description query | 0.153322 ms | 0.080031 ms | 112,373 | 30,246 |
| 512 tools, description query | 1.688018 ms | 0.463818 ms | 790,476 | 133,271 |
| 64 tools, exact name | 0.008599 ms | 0.008302 ms | 5,594 | 5,594 |
| 512 tools, exact name | 0.010087 ms | 0.009113 ms | 5,601 | 5,602 |

Description queries spend about 48% and 73% less time, with about 73% and 83%
fewer allocated bytes respectively. Allocation counts fall from 844 to 333
and from 4,883 to 785. Exact lookup retains 92 allocations; its small timing
variation is not attributed to this change.

The benchmark invokes the complete tool_search executor: argument decoding,
lookup/ranking, activation, signature/schema output and response encoding. The
fixture alternates four plausible file/project/document descriptions, returns
three matches, and has no FTS index. Registry construction is outside the timed
loop. Repeated calls use the same query and registry; tools are already active
after the first call. The returned tools themselves are not executed.

These are local synthetic tool-call timings, not end-to-end agent latency or
token savings. Benefits depend on catalog size, descriptions, query terms and
whether lexical fallback is reached.

## Validation

An independent copy of the previous lexical implementation provides a parity
oracle in tests. Coverage includes:

- exact token parity for punctuation, stopwords, Unicode lowercasing, malformed
  UTF-8, all byte values and 500 deterministic random byte strings;
- result names, server labels, scores and order across repeated query words,
  ties, empty/unmatched queries and six limit values;
- exclusion of gateway tools, late registration and registry replacement;
- full response signatures/schemas and activation of only the selected tools
  with a missing or failed index;
- concurrent independent queries against the same registry.

Focused search tests, full go test ./... and go vet ./... passed.

    go test ./internal/tools/search
    go test ./internal/tools/search -run '^$' -bench '^BenchmarkToolDiscovery$' -benchtime=500x -count=3

App-local evidence: .tmp/tool-discovery-cost/paired.json, before-overlay.json,
tool_searcher.go.before, benchmark-builds.json and checks.json. Before/after
test executables are retained alongside the evidence.
