# Long-session cache and compaction profile (2026-10-19)

Measured on isolated copies of the existing 11,894-message archive. Live calls
used the session's Codex provider, `gpt-6.1-sol`, and unchanged `xhigh` effort.
No original conversation/memory database or authentication state was modified.
There were three live requests: an identical foreground pair, then one ordinary
production summarizer call. No tools or old tasks were executed.

## Cache: warm reuse confirmed

| Request | Input | Cached input | Output / reasoning | Total seconds |
| --- | ---: | ---: | ---: | ---: |
| First foreground audit | 104,297 | 0 | 886 / 827 | 23.61 |
| Byte-identical second audit | 104,297 | 104,064 | 1,095 / 1,023 | 27.26 |

The second request reused **99.78%** of input. The actual request SHA-256, input
SHA-256, instructions, effort and cache key matched. Body key and `session_id`
header agreed. The independent raw SSE usage tap and normal provider parser
reported the same cached token counts. Zero in the earlier tests was therefore
not evidence of a broken parser or globally disabled cache. The exact cause of
every historical miss cannot be reconstructed without those old request hashes.

This pair does not establish a latency improvement: generated output/reasoning
lengths differed, and server load was uncontrolled. A hit is not a guarantee of
a shorter total answer. Compaction also replaces part of the input prefix, so
the first new-prefix request cannot be compared as if its old cache were intact.

## What remains after compaction

The offline view has 98 messages including two diagnostic system/context
messages. Its local estimate is 106,203 tokens; the actual live audit reports
104,297. Components below use the same local estimator and sum exactly once,
including message framing. They are **estimates, not provider billing shares**.

| Component | Estimated tokens | Share |
| --- | ---: | ---: |
| Native model continuation/reasoning state | 57,615 | 54.25% |
| Tool results | 31,978 | 30.11% |
| Tool names and arguments | 10,259 | 9.66% |
| Exact historical user-context JSON | 2,432 | 2.29% |
| Visible assistant text | 2,026 | 1.91% |
| Summary, retained facts and framing | 1,281 | 1.21% |
| Recent user text | 476 | 0.45% |
| System/context text | 136 | 0.13% |

No byte-identical native reasoning blocks were found. The compactor preserves
the recent user turns and their protocol state. The retained context contains
eight original requests and one legacy state block. Removing these components
for a lower token count would change the current retention guarantees; this
diagnostic makes no such runtime change.

## Summary latency

The production helper received 135,402 transcript bytes, reported 40,483 input
tokens, and generated 1,076 output tokens including 516 reasoning tokens.
Its exact historical user context was checked after summarization.

| Observed event | Seconds from HTTP request start |
| --- | ---: |
| HTTP response / `response.created` | 0.94 |
| First `response.output_item.added` | 2.44 |
| First visible text delta | 16.77 |
| `response.completed` | 28.71 |

Thus 14.33 seconds elapsed between the first output item and visible summary,
and another 11.94 seconds until completion. These are client-observed stage
boundaries, not server-internal queue/prefill counters. They cannot isolate
queueing from prompt processing exactly. The helper's input rendering takes
less than one millisecond per pass in the offline repeated-render measurement;
it cannot explain a tens-of-seconds wait. The previous 23.44-second sample and
this 28.71-second sample do not demonstrate a compaction speedup.

## Changes and verification

Added opt-in, content-free profiling tests and a passive SSE usage/timing tap.
Reports contain counts, hashes and numeric usage, never prompts, generated text,
encrypted reasoning or credentials. The tap preserves every stream byte and
distinguishes an omitted cache field from an explicit zero. Synthetic tests
cover fragmented SSE, token-accounting totals and non-mutation of input history.

Normal `go test` skips all real-data/live fixtures. Offline runs require
`SUPERCLI_CONTEXT_PROFILE_STAGE` and `SUPERCLI_CONTEXT_COMPACTED_STAGE`, pointing
to separate prepared fixtures with `fixture.json`. The paid/subscription live
test additionally requires `SUPERCLI_CONTEXT_CACHE_LIVE=1`. It refuses to rotate
the original OAuth login and uses the fixture's configured model and effort.

Validation: live three-request audit passed; offline original/compacted profile
passed; `go test ./internal/agent ./internal/llm` and scoped `go vet` passed.
Existing instruction-retention tests, including repeated compaction, also ran.
The initial synthetic timing test assumed sub-tick Windows timer resolution;
its fixture now starts one second in the past without adding a blocking sleep.

No runtime behavior, reasoning setting, executable or release was changed.
Further optimization should be justified by a reproducible runtime bottleneck.
The current data does not justify cutting retained facts or opaque protocol state.

Reference for general prefix-cache behavior:
[OpenAI prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching).
The live result above, rather than public-API schema assumptions, establishes
what this Codex backend returned for these requests.
