# Stats panel and search context allocation audit — 2026-10-09

This batch does not add model instructions, change tool schemas, discard conversation history, or modify the OpenCode Zen transport. The console/Stop cancellation fixes validated in the preceding batch remain intact.

## Session statistics

The panel previously read all 25 columns of every usage record into a retained slice before calculating totals. The new ordered visitor scans the 16 fields needed by the panel, quotes each call immediately, and keeps totals plus the final call's identity/context. Prices are still added in original call order: no grouped-rate approximation, cap, or cross-session cache. Legacy and preview-only cost behavior is preserved. Export and full usage-history reads are unchanged.

A read-only metadata query found sessions with up to 1752 calls in the local store. The benchmark below uses synthetic records and the real stats entry point; it contains no conversation text, API keys, or model calls.

| Calls | Before median | After median | Before allocated bytes | After allocated bytes |
| --- | ---: | ---: | ---: | ---: |
| 8 | 0.588 ms | 0.565 ms | 31,413 | 25,337 |
| 1752 | 6.886 ms | 4.853 ms | 2,628,898 | 957,137 |
| 10000 | 38.435 ms | 26.151 ms | 20,402,192 | 5,366,893 |

Three runs per fixture, 150 ms benchmark duration, Windows amd64, GOMAXPROCS=2; fixture inserts excluded from timing. Allocated bytes are per panel read, not process RSS or retained heap. Provider prompt processing and tokens per second were not measured in this batch.

Tests compare ordered projection, all token totals, known flags, last-call context/identity, exact cost values against the original resolver, mixed pricing, legacy/preview behavior, cancellation and query errors. Failed reads do not expose partial totals.

## Explicit search context

For captured hits and a positive explicitly requested context, the final context renderer replaces the entire preview result. Skip only the discarded preview construction. Automatic and location-only search retain their policy. Full Result parity tests cover ordinary and minified files, the 500-match cap, no hits, clamped radius, missing files and cancellation.

The real run benchmark, compared through an exact original-source overlay, shows minified explicit-context allocations falling from 605 to 520 per call and about 564 KB to 540 KB. Grouped explicit-context allocations fall from 636 to 622. Sparse and capped searches have effectively unchanged allocations. Small timing changes are within a few percent and are not presented as an agent end-to-end speedup. Model-visible output and retained output are identical.

The Windows synthetic-rg test copies its executable rather than hard-linking the running test image; otherwise Windows prevents TempDir cleanup even after the marked child exits.

## Portable auxiliary CLIs

Default evaluation workspaces and wire logs now use the existing runtime data root resolver: eval/workspaces and logs/wire.jsonl underneath the portable data directory. Explicit paths keep their semantics. Validation does not create directories. Resolver/directory errors are surfaced rather than silently falling back to an OS temporary/profile directory.

## Validation scope

Fresh tests and vet cover eleven packages: usagecost, credits, session storage, webgui, search, tools, app, TUI, agenteval, supercli-eval and supercli-wireprobe. An exact source reconstruction verifies that every other runtime/UI source is unchanged from the preceding installed batch, including its 231 passing UI tests and console/Stop fixes. Cross-builds cover Windows amd64, Linux amd64/arm64 and macOS amd64/arm64, both TUI and GUI.

The paragraph HTML-cache proposal is deferred: its persistent-REPL experiment failed the positive GC control and cannot establish an exclusive memory saving. No frontend modification was adopted on that evidence.
