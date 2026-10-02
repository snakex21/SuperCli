# History, schema residency and command-evidence audit — 2026-10-02

This audit reduces retained memory and duplicated tool evidence without adding an LLM call, prompt instruction, provider-specific route, or history classifier. OpenCode Zen routing and headers remain unchanged. App data stays in the portable application tree.

## Replaced conversation arrays

Compact and resume previously reused the old message slice. A shorter slice still owned its backing array, which held discarded message payloads beyond its length. Both operations now allocate an exactly-sized replacement and copy only retained messages. They do not mutate external archived views.

A separate Go process used 64 independently allocated assistant messages of 512 KiB each. The final live history contained two messages. Heap after GC:

| Operation | Before | After |
| --- | ---: | ---: |
| Compact | 34,484,200 B | 930,056 B |
| Resume | 34,489,456 B | 930,328 B |

This is about 32 MiB less retained heap in this synthetic replacement fixture. It is not a claim about startup RAM, total WebView2 process memory, or token savings. Canonical saved history and intentional external archive views remain intact; GC can reclaim old data when those views are no longer referenced.

Regression coverage includes chronological messages, multiple leading system messages, images, opaque native reasoning, active tool call/result pairs, command errors, hidden indices, aliased archive input, empty history, and no-op compaction.

## GUI formatting

The GUI now reuses native Intl number/date formatters for the current language. The cache is bounded to 32 entries and discards old-language entries when formatting in a different language. Failed constructors are not cached, and existing fallbacks remain available.

An isolated Node V8/ICU fixture formats 200 synthetic rows (integer, compact number, two currency amounts, and date per row), with one warm-up followed by seven passes. Median formatting time was 37.78 ms before and 2.33 ms after. The measured seven warm passes constructed 5,600 NumberFormat and 1,400 DateTimeFormat objects before, and zero additional formatters after. Output checksums were identical.

This measures formatting CPU work only, not DOM rendering, animation frame rate, or WebView2 RSS. All 27 supported languages were compared with uncached Intl output, including rounding/precision thresholds, currencies, negative values, locale changes, invalid inputs, fallback/retry, and bounded currency churn.

## Successful command evidence

For a small successful ctx_execute result, the model-facing JSON may omit command only when its echo is at least 256 bytes and exactly equals the executed/coerced argv joined with spaces. The paired assistant tool call still contains every argument. Absolute resolved workdir, stdout, stderr, exit status, duration, truncation and capture diagnostics remain unchanged. Failed results, altered/wrapped commands, short commands, previews and retained/large output keep their existing paths. Live Result.Text and serialization remain full; stored model history has the equivalent call-plus-result representation.

The private fixture contains a 481-byte command/script and a short search hit. Actual Go JSON output shrank from 722 to 226 bytes. Serialization through the actual OpenAI client using a fake in-process HTTP transport shrank the native request from 1,703 to 1,199 bytes and the invoke_tool request from 1,739 to 1,235 bytes. Only the result content differs; all paired call arguments and other request settings are identical. The additional small JSON encoding costs about 0.979 microseconds on the long-command fixture; short commands bypass it (0 allocations).

Two real local Qwen calls used qwen3.8-27b-uncensored, reasoning_effort=none, temperature=0, seed=2718, max_tokens=256 and disabled further tools. Input was a completed private tool call/result and a request for six facts. Prompt tokens were 928 before and 763 after (165 fewer); both answers were identical and all six facts correct. No real command was executed by the model. The before/after order, warm caches and two samples do not support an end-to-end timing claim or a claim about fewer agent turns.

Parser audit and tests preserve failed-check evidence, exact original UI text, output-persistence guards, coercion, and native/envelope call pairing. The tool schema and description do not grow.

## Deferred comparison finding

Codex keeps inactive worker residency bounded by persisting a rollout and restoring it on demand. SuperCli child loops currently have no writer for a complete resumable history, and its existing eviction removes continuation. A disk-backed worker restoration feature therefore needs separate portable persistence and equivalent replay tests. This audit does not lower the worker count, discard history, or clear active loops merely to reduce RAM.

## Schema cache residency

The normalized tool-schema cache now has both its existing 512-entry limit and an 8 MiB owned-payload budget. This follows the count-plus-byte bound used by the adjacent Claude harness's file-state cache. The payload includes detached raw schema keys and encoded forms; map/order overhead is outside that budget. Oversized schemas still compile fully and correctly, without entering the cache. FIFO eviction releases the required prefix and clears unused queue slots. Cached outputs stay immutable; every caller receives independent bytes.

44 measured static schema literals occupied 121,659 bytes in both transport forms (88 entries), well below the limit. This subset excludes dynamic/user/MCP schemas. A separate process churned through 100 different ~256 KiB schemas in both forms:

| Metric | Before | After |
| --- | ---: | ---: |
| Retained heap after GC (median) | 81,395,712 B | 8,389,240 B |
| Owned cache payload | 104,887,160 B | 7,866,540 B |
| Cold 100-schema churn | 301.73 ms | 300.27 ms |

This is a memory-residency bound, not a claim of faster execution in every scenario or a promise about ordinary startup RSS. A changed schema working set larger than the budget can require recompilation. Default-GC 32 KiB cold churn measured 391.90 to 451.73 microseconds (+59.83 microseconds, +15.3%). A controlled CPU comparison found the largest measured static builtin (7,321 bytes) hot lookup at 701.4 to 821.3 ns (+119.9 ns, +17.1%). Detaching the cache key avoids retaining a larger parent string, at the cost of content comparison instead of pointer identity. Tiny hot lookup remained approximately 67 to 69 ns with the same single allocation. No production GC settings were changed; controlled benchmark settings are not app defaults.

Tests cover count and exact byte boundaries, bulk eviction, cleared backing queue slots, full/portable JSON equivalence, oversized bypass, input detachment, concurrent identical and different-key misses, caller mutation isolation, and byte accounting. The scoped cache suite also passed with the race detector and shuffled test order.

## Combined validation and builds

- PASS: full go test ./... including agent, tools, LLM, storage, TUI, web GUI and command packages.
- PASS: 96 frontend tests, including the four new formatter cases.
- PASS: scoped schema cache race detector (shuffle seed 12345).
- PASS: git diff --check with CRLF-aware whitespace settings.
- PASS: CLI and GUI builds for Windows amd64, Linux amd64 and macOS arm64; stripped with -s -w and -trimpath, Windows GUI with -H=windowsgui.
- PASS: both Windows --help smoke checks.
- PASS: installed Windows EXE hashes match the new build artifacts; prior EXEs are backed up in the private audit directory.

Linux/macOS cross-build success does not establish runtime UI behavior on those systems. GUI/TUI use the shared backend memory/tool-result changes; Intl reuse applies to GUI. The production/test sources owned by this audit remained byte-identical throughout combined tests and builds. This round does not prove fewer complete agent turns.

Private reproducibility data is under .tmp/memory-context-audit-2026-10-02 (Qwen requests/raw responses/assessment, formatter source/benchmark/results, combined tests, build/install hashes), .tmp/context-projection-audit-2026-10-02 (isolated message-retention measurement), .tmp/ctx-command-modeltext-2026-10-02 (actual Go result and protocol fixtures), and .tmp/schema-residency-2026-10-02 (baseline/after cache measurements and scoped race checks).
