# Focused source search (2026-09-23)

## Evidence and change

The latest 120 persisted assistant messages containing search_code held 135 search
calls. In 13, the returned line count equaled the requested match limit with no
notice that searching had stopped. This is evidence of an ambiguous result, not
proof that all 13 caused repetition. No private session content was sent to the
experiment models.

The local DeepSeek Harness, Pi and OpenCode implementations expose file glob
filters and explicit result-limit notices. Relevant inspected sources:

- [DeepSeek grep](../../../deepseek-harness-master/packages/fs/tool-fs-search/src/grep.ts)
- [Pi grep](../../../pi-main/packages/coding-agent/src/core/tools/grep.ts)
- [OpenCode grep](../../../opencode-dev/packages/opencode/src/tool/grep.ts)

SuperCLI now accepts an optional include glob in search_code, e.g. *.go,
src/**/*.ts, or *.{go,zig}. Patterns with no slash match basenames at any
level; patterns with slashes are relative to the requested search root. Matching
is case-sensitive. One positive glob is accepted, with at most 32 brace
alternatives and 512 pattern bytes; nested braces and negated whole globs are
rejected. Character classes use Go path.Match syntax, including [^a] negation.

The Go scanner excludes nonmatching files before opening them. The rg backend
passes the filter directly as an argv element and applies the same matcher to
returned paths. Existing skipped directories stay excluded. The existing
backend-specific handling of hidden files/gitignore is unchanged; this does
not introduce full gitignore support into the Go fallback.

When the match limit is reached, both backends say results **may be incomplete**.
No extra scan is made merely to prove a further match exists. Numbered context
preserves the notice. Search failure fallback preserves the file filter.

This is shared tool code for CLI/TUI/GUI and local/cloud models. There is no new
system instruction, helper inference, persistent index, worker or OS process
on the Go fallback path. The serialized search ToolDef measured in the live
fixture grew from 837 to 867 bytes (+30 bytes); actual provider token counts
are reported below. The optional filter therefore has a small schema cost,
not a claim of zero added tokens. OpenCode Zen transport/headers were not edited.

## Regression and microbenchmark

A pre-change regression reproduced 20 documentation hits crowding out a Go
definition with no indication of the limit. After the change, a filtered search
finds the definition and neighboring code in one tool execution.

A second fixed corpus has 128 Markdown files (512 lines each) and one small Go
source. Both variants find exactly the same definition. Go fallback benchmark,
Windows/Go 1.26.2, Ryzen 7 5800X3D, three runs of 20 iterations:

| Variant | Median time | Range | Median allocated bytes |
|---|---:|---:|---:|
| Scan all files | 14.164 ms | 13.076–14.687 ms | 206,760 |
| include=*.go | 0.500 ms | 0.482–0.540 ms | 144,626 |

This measures CPU/file traversal and scanning with a warm filesystem. It is not
a 28x whole-agent speedup, a GPU benchmark, or evidence of fewer model turns.

## Live experiment

Two independent models inspected a newly created synthetic repository using the
real agent.Loop, search_code, list_dir, read_lines and read_many. No shell/edit
capability, user repository, user configuration or private history was exposed.
Both variants had identical system/user prompts, model reasoning setting low,
3072-token local completion budget, 8-step cap and request timeouts. They had to
locate ResolveWidget in Go and report its path and two exact return values.
Markdown deliberately included repeated stale mentions of the symbol.

Before/after executables were built around the same experiment driver, with the
production search implementation from before/after this change. Different model
providers ran independently; requests to each individual model were sequential.
Cloud order was after/before, then before/after. Local order was before/after
twice. These are small, unblinded smoke comparisons, not statistical estimates.

| Model / attempt | Variant | Model turns | Tool calls | Input tokens | Output tokens | Wall time | Correct |
|---|---|---:|---:|---:|---:|---:|---|
| Muse 1 | before | 5 | 4 | 13,792 | 514 | 8.369 s | yes |
| Muse 1 | after | 4 | 3 | 9,884 | 385 | 7.677 s | yes |
| Muse 2 | before | 5 | 7 | 15,127 | 599 | 6.998 s | yes |
| Muse 2 | after | 4 | 3 | 9,873 | 380 | 9.738 s | yes |
| Qwen 1 | before | 1 attempted | 0 | unavailable | unavailable | 120.274 s | timeout |
| Qwen 1 | after | 3 | 2 | 4,079 | 487 | 25.176 s | yes |
| Qwen 2 | before | 3 | 2 | 3,998 | 234 | 15.584 s | yes |
| Qwen 2 | after | 3 | 2 | 4,094 | 273 | 14.241 s | yes |

Muse used the new glob after its first broad search reached the limit. Across
its two pairs, model turns fell 10 -> 8, tool calls 11 -> 6, and total input
28,919 -> 19,757 (~31.7% lower). Reported cached input was 18,696 -> 13,222;
subtracting it gives 10,223 -> 6,535 noncached input tokens. Those counts do not
establish a monetary saving on a free endpoint. Total wall time increased
15.367 -> 17.415 seconds: **no consistent latency win was established**.

Qwen already chose a specific function regex before the change. Both successful
variants needed 3 turns/2 tool calls; the comparable pair used slightly more
input/output tokens after the change. Its initial baseline timed out before any
tool call and had no reported usage. That failure cannot establish a search
speed benefit. All seven completed runs returned the correct facts; one timed
out. No claim about complex implementation tasks or universal model behavior
is justified by this fixture.

## Validation and reproduction

Regression coverage includes root-relative and basename glob semantics, **,
braces, invalid patterns, bounded expansion, explicitly named files, filter
before file read, retained limits in context, rg failure fallback, and both
native and sentinel agent routes.

A real [ripgrep 15.2.0 release](https://github.com/BurntSushi/ripgrep/releases/tag/15.2.0)
was downloaded only into the app's .tmp/search-focus/rg directory. The Windows
archive SHA-256 matched its official GitHub release digest:
71b2fef860abe467217a538ff31de02f5258807c0129f771846f87bd029aafc5.
Eight glob patterns produced identical results with this binary and the Go
scanner. It was not installed into PATH or added to the application bundle.

Commands:

    go test ./internal/tools/search ./internal/agent
    go test ./internal/tools/search -run '^$' -bench '^BenchmarkSearchInclude$' -benchmem -benchtime=20x -count=3
    # Optional real-rg integration: set SUPERCLI_TEST_RG to the executable path.
    go test ./internal/tools/search -run '^TestSearchIncludeRealRipgrepParity$' -count=1 -v
    go build -o .tmp/search-focus/after.exe ./scripts/search-focus
    .tmp/search-focus/after.exe qwen3.8-27b-uncensored <absolute-app-local-output-dir>
    .tmp/search-focus/after.exe muse-spark-1.3-contributor-free <absolute-app-local-output-dir>

Raw local evidence is under .tmp/search-focus: before-sources.json,
baseline-regression.json, microbenchmark.json, rg-parity.json and the eight
model/variant JSON results. The before executable was built before editing the
search implementation; it is retained there for the local paired experiment.

Final validation: all 64 tested packages passed, go vet ./... passed, and
git diff --check passed.
