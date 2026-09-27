# Measure generated output instead of stream metadata — 2026-09-27

## Evidence

Both Metered and the agent loop started TTFT on any non-notice delta. The Chat Completions decoder forwards role-only frames before content. Usage-only, empty, finish and error frames also satisfied the timing check. A controlled 20 ms metadata-only fixture incorrectly recorded about 585,000 evaluated tokens/s and trained an 8.78-million-token candidate despite receiving no model output. These are regression-fixture values, not observed provider performance.

TTFT feeds the shared adaptive context budget. Treating early metadata as output can hide a long wait. Simply waiting for a complete ToolCall would create the opposite error: providers buffer arguments until completion, so generation time would be attributed to waiting.

## Change

- Delta.HasModelOutput recognizes text, exposed/native reasoning, complete tools and an internal OutputStarted marker. Role/usage/status/error-only frames do not start TTFT.
- Chat Completions, Responses and Anthropic announce the first generated tool name/argument fragment with at most one marker per stream. Complete tool calls remain buffered and execute only after their full arguments arrive.
- Metered and the agent loop use the same output predicate. A metadata-only response retains zero TTFT and cannot train a prefill profile.
- Markers add no conversation text, UI messages, tool executions, prompt tokens or provider calls. Existing usage/error forwarding remains intact.
- The opt-in coding evaluation saves existing per-call CallStats, allowing a wait/stream breakdown without production telemetry expansion.

Provider request formats, authorization and the special OpenCode Zen route are unchanged. Portable profile storage and previous user data are preserved; historical measurements are not rewritten.

## Verification

Before the fix, role/usage/finish/error/empty-frame cases failed and the loop learned a profile from metadata. After the fix, all pass. Gated real HTTP/SSE fixtures for all three protocol decoders verify that timing begins after a controlled generation delay, the output marker arrives before the complete tool, the complete tool call is emitted exactly once, and usage survives. Agent tests verify metadata does not end backend wait, and progress cannot enter the transcript or create tool calls.

Full go test ./... (90-second package timeout), go vet ./..., both portable builds, CLI --help smoke and git diff --check pass. Artifacts: .tmp/output-start-timing-2026-09-27/.

## Local coding probe

One isolated production-path trial on qwen3.8-27b-uncensored corrected the cache-expiry boundary, passed the independent existing tests, and left test source unchanged. Thin protocol, deferred schemas, low reasoning, temperature 0, seed 20260926; no real user project files were included.

| Call | Input | Output | First output (s) | Whole stream (s) |
| --- | ---: | ---: | ---: | ---: |
| 1 | 2955 | 86 | 6.887 | 9.900 |
| 2 | 3330 | 69 | 4.460 | 6.722 |
| 3 | 3771 | 268 | 4.049 | 13.240 |
| 4 | 4333 | 129 | 4.381 | 8.909 |
| 5 | 4607 | 315 | 3.721 | 15.092 |

Total worker duration: 54.610 s; 18,996 input and 867 output tokens, five provider calls, no gate rejections. Wait totals 23.497 s and remaining stream duration 30.367 s; approximately 0.747 s lies outside metered provider durations. Zero reported cached tokens is not proof that the backend reused no cache.

This is a successful compatibility/profiling probe, not an A/B speedup result. TTFT still includes queueing, transport, prefill and any hidden work before the first exposed output. Stream time is likewise an observed interval, not a pure GPU generation measurement. Providers that expose only a completed output still limit timing precision. New correct samples gradually update existing profiles.

## Investigation not shipped

The existing long-context preparation benchmark (~97,460 estimated history tokens) measured 52.4 microseconds native and 46.0 microseconds thin, with 19/21 allocations respectively. The profile did not justify a new caching/invalidation layer. Raw CPU/heap profiles and benchmark output are under .tmp/context-prepare-profile-2026-09-26/.

## Installation

Both executables were replaced from verified builds; installed.json records SHA-256 hashes and backups. Existing processes were not terminated. Restart CLI/GUI to load the update. The broader efficiency goal remains active.
