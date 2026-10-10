# Frontend and prefill follow-up — 2026-10-09

This pass adds stronger measurements and repairs an obsolete development verifier. It introduces no new runtime mechanism, prompt instruction, model tool or dependency in SuperCli. The installed GUI/TUI production sources and EXE checksums remain those from the preceding verified batch.

## Real-browser regression and stream measurement

The optional browser gate initially failed because the parser reference commit predates the code-copy button. Both incremental DOM parity and raw renderer equality compared the intentional button against a version without it. scripts/transcript-performance-checks.cjs now compares canonical Markdown DOM while ignoring only direct pre > button.code-copy controls. Existing code-copy behavior/accessibility tests remain in the regular UI gate. No production renderer code was changed.

The actual frontend was served on loopback with synthetic APIs and run in headless Chrome 155.0.8059.39 on Windows. Browser profile/TMP/TEMP stayed inside the portable evaluation folder. The fixture invoked no backend model, tool or transcript write. Correctness passed all 933 tested prefixes, reasoning disclosure (6 folded versus 4,006 expanded elements), source replacement, stable row identities and tool-boundary ordering.

Five equal-work runs used the existing synthetic SSE fixture: 73,942 source characters, 289 chunks, requested 256-character chunks every 8 ms. Every run retained the complete exact source and 3,526 final elements.

| Measurement | Result |
|---|---:|
| Median of per-run DOM-commit p50 | 3.0 ms |
| Median of per-run DOM-commit p95 | 5.7 ms |
| Largest observed DOM-commit delay | 17.6 ms |
| Median cumulative renderAssistant CPU time per run | 45.8 ms |
| Long tasks observed across all five runs | 0 |
| Elapsed time per run | 2.662–2.738 s |

These numbers measure packet-arrival to a DOM update in headless Chrome. They are not compositor paint, interactive typing latency, WebView2 FPS/RSS, a before/after speedup, or a proof that sparse real provider packets cannot pause. The fixture's requested timer cadence is not a claim about precise network intervals. Canonical source, final rendering and ordering passed before accepting the measurements.

Portable artifacts: .tmp/optimization-oct8-2026/chromium-latency-audit, including correctness-before-normalization.log, correctness-before-raw-normalization.log, correctness.log, stream-measurements.json, stream-driver.cjs, run-stream.cjs and ui-final.log. The existing real-browser script was used unchanged; the supplemental measurement driver is ignored development data.

## Prefill profile persistence — measured and rejected

The production portable profile file contains 29 entries and 11,392 bytes. Observe synchronously serializes/sorts/writes all profiles after an eligible successful model response while holding its mutex. Tiny messages below 4,000 input tokens skip Observe entirely.

A matched Go overlay seeded 29 synthetic profiles and measured 200 updates in each of three repetitions. The production median was 1,969,967 ns/op, 23,806 B/op, 14 allocations. A measurement-only version omitting saveLocked measured 121 ns/op, 24 B/op, one allocation. Both updated the same in-memory profile counters. The counterfactual omits durability and is not a valid production substitute.

Approximately 2 ms after a response does not explain the reported multi-second prompt delay; introducing a timer/background writer or losing the latest durable sample is not justified by this measurement. No production edit was adopted. These are warm local filesystem measurements, not a general guarantee for slower disks or another machine. Artifacts: prefill-persistence-audit/experiment_test.go, overlays, decision.json and go-prefill-persistence-oct9-{0,1}.log in the portable evaluation folder.

A read-only metadata query selected 12 recent sessions whose last stored message is assistant and which have completed turn records. In the recent Qwen session, five completed turns/seven model calls had 6.721 ms context_prepare plus 0.556 ms request_encode, 19.250 s backend_wait and 77.543 s stream_total. The last two-turn Space Bunny sample had 1.077 ms context_prepare plus 0.528 ms request_encode, 2.012 s backend_wait and 5.678 s stream_total. These are historical accumulated measurements, not current-wire A/B or proof that backend_wait is pure prefill. Provider wait includes scheduling/network/first output, and nested tool/persistence phases overlap; they must not be added blindly to wall time. Sanitized query and results: prefill-persistence-audit/phase-audit.cjs and phase-audit.jsonl.

## Live model follow-up — limited result

LM Studio refused the connection, so no live Qwen comparison was made. The public Kilo catalog responded successfully. Two synthetic production-serializer Chat requests with eight 872-byte remember notes were submitted to free liquid/lfm-2.5-2.6b:free without a user key. With the original result echoes the provider reported 7,254 prompt tokens; with receipts it reported 3,790, a reduction of 3,464 tokens (47.75%). This is tokenizer-specific provider telemetry, not a universal token conversion from wire bytes.

Both initial replies exhausted a 64-token generation limit entirely before a final answer. A follow-up at 1,024 tokens also exhausted its reasoning allowance in both variants. Thus answer quality/completion did not pass and timings are not accepted as a speed comparison. The subsequent request order/cache differed (one follow-up reported 3,776 cached tokens), further preventing a clean timing comparison. A reasoning=none experiment returned HTTP 400; support for a parameter does not establish support for every effort value. No provider configuration or production reasoning path was changed.

A second model, cohere/north-mini-code:free, also failed the completed-answer gate on the first candidate request; the baseline was not sent. Its reported counters were inconsistent (reasoning tokens exceeded completion tokens), so that result is excluded from token-savings evidence. These outcomes do not prove a SuperCli regression or that receipt projection degrades model quality. Exact saved note/argument parity and all three request formats remain covered by the preceding production tests. No success is inferred from these incomplete model replies.

Artifacts: remember-wire/kilo-live-pair.json, kilo-live-completed-pair.json (HTTP 400 attempt), kilo-live-quality-pair.json and kilo-north-pair.json under the portable evaluation folder. Only synthetic test data was submitted. No new model call or instruction was introduced into ordinary application runs.

## Verification and unchanged installation

All 222 dependency-free UI tests passed after updating the verifier. Real Chromium correctness and all five stream runs passed. git diff --check passed. The sole covered-source change from the previous sealed validation is scripts/transcript-performance-checks.cjs; replacing its bytes in the digest with the saved pre-edit version reproduces the exact prior source hash. Production code is unchanged, so Go suites and ten binaries were not needlessly rebuilt.

Current covered-source digest: 39624f59af6eda2f8b98677bb1ad2cdae72bdd40c934e0e18279d09f083b181f. Reproduced validated runtime-source digest: 80f7e3a68f5659a387509bc5e383e2c16de265850abbec764aefe5862d5a277e. Runtime proof: chromium-latency-audit/runtime-source-proof.json.

Both installed Windows EXEs still match their verified hashes: TUI b2b5a616dfe82a4ae2c04bce2d57c8ea9bc83429e4fda701918e4ca44686f6b1; GUI 726046d37206293ac4f1912afa8493466b40366bc9eeb9ac0faf4c7a30ee9dbf. No user application was closed/restarted, and no commit, push or release was made.
