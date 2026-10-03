# Request preparation audit: avoid unused automatic split scans

Date 2026-10-03. Baseline clean main 5524acf, SuperCli dev19.
The frozen read-only prototype was integrated as the minimal branch choice after these measurements. No provider/network request, model/config/cache change, or active-user-operation polling was used.

## Candidate

The public Run → runStep → maybeAutoCompact call path enters context defense before each main provider request. Below its effective compaction threshold it returns immediately. Above the threshold, current window_compact.go builds compactionHistory and calls history.compactSplit(w); when reason is empty (ordinary automatic defense), the very next branch discards that result and calls history.autoCompactSplit().

compactSplit counts the protected tail with llm.EstimateTokens, sometimes twice. autoCompactSplit only needs real user-turn boundaries and preserves the active turn plus preceding instruction. The unused count remains paid even when automatic defense correctly refuses to compact a one- or two-turn active task.

The integrated exact-source overlay chooses one existing helper:
- ordinary automatic reason == "": autoCompactSplit only;
- explicit nonempty reason, including provider context-limit recovery: compactSplit exactly as before.

No new helper state, cache, fields, lifecycle invalidation, request tokens, prompts, summary calls or history policy. Manual CompactNow is untouched. Source baseline SHA-256:
0f481b11785de5e464c481453e326e509070c7d8af7f0be352196b0f86b7a779
Candidate:
2d8691565561e3360f9854d4025ff49c92b2fd3f39aea8aae189dd6ac4b3168d

## Fixture and scope

Windows amd64, Ryzen 7 5800X3D, Go portable offline module/cache setup, GOMAXPROCS=2. One ABBA sequence, 100ms/sample, no concurrent CPU benchmark. Tests in both overlays completed PASS (0.150/0.177s); full runner terminal exit 0.

The fixture uses current contextPreparationFixture's stable thin registry and existing recordingWriter/stubProvider, actual current maybeAutoCompact and full defense + definition/message preparation. Pruning is disabled in this isolated benchmark so it does not independently rewrite results before the measured automatic-compaction branch. Tool-result text is synthetic code-shaped text; calls/IDs are synthetic. Maximum individual text 11,348 B is grounded only in the largest original tool content of a previously completed saved S14 archive (1,859 rows). This does not prove incidence of four or 32 such results in a live active turn, nor reconstruct that archived run's hidden/pruned state.

Automatic benchmark cases retain one current user turn and unresolved assistant/tool exchanges. They do not produce a summary or mutate history. The two-protected-turn case also leaves history unchanged. A 32-result case is stress, not a typical request-size claim. Estimates include full fixture schema/catalog overhead. These are local estimator counts, not actual tokenizer or provider counts.

## Counterbalanced results

Two baseline and two candidate samples per row. Ranges are reported rather than treating two samples as a statistical distribution.

| Actual preparation sequence | Baseline µs/op | Candidate µs/op | B/op, allocs/op unchanged |
| --- | ---: | ---: | ---: |
| Below threshold, 1×1,024 B | 4.979–5.086 | 4.908–4.971 | 8,129; 12 |
| Active 4×11,348 B; estimate 15,120 tokens | 8.411–9.651 | 5.648–6.660 | 8,833; 12 |
| Active 32×11,348 B; estimate 103,124 tokens (stress) | 31.479–36.174 | 6.974–8.070 | 15,617–15,618; 12 |

The direct maybeAutoCompact measurement excludes later definition/request assembly:

| Direct automatic defense | Baseline µs/op | Candidate µs/op | B/op; allocs/op |
| --- | ---: | ---: | ---: |
| Below threshold | 1.105–1.224 | 1.150–1.191 | 248; 4 |
| Four active results | 4.530–4.965 | 1.259–1.365 | 248; 4 |
| 32 active results (stress) | 25.871–28.696 | 1.569–1.665 | 248; 4 |
| Two protected turns, 8 results; estimate 29,063 | 14.125–15.641 | 1.373–1.407 | 248; 4 |

Meaningful effect is removal of redundant CPU scans above the threshold. No RAM/allocation reduction, token reduction, extra/fewer agent turns, live prefill/TTFT/TPS or GUI/TUI FPS/RSS gain is claimed. Below-threshold measurements overlap and show no meaningful benefit.

## Exact behavior proof

The private test includes an unchanged source copy of baseline maybeAutoCompact for differential comparison. Thirteen cases compare exact complete messages/hidden flags, compaction event values, summary input evidence, request estimates and message JSON hashes:
- below threshold; one active turn; two protected turns;
- successful, failed and unavailable automatic summaries;
- calibrated provider+delta estimate;
- hidden visibility with native signed-looking Anthropic replay parts;
- chat/advisor projection;
- explicit context-limit recovery and failed emergency summary;
- cancelled summary with deterministic nil-output delivery.

The initial cancellation test offered both a ready buffered event channel and an already-cancelled context, so the existing select could randomly deliver an event or cancellation in either implementation. Its fixture was corrected to a nil output channel, making the cancellation outcome deterministic; no production behavior was changed to satisfy it.

The public Loop.Run offline proof completes with one main scripted provider call, identical declared usage (100 input / 4 output), no AutoCompactEvent and unchanged complete preexisting evidence. It executes no tools, screenshots or programs. This proves reachability and preservation, not a live timing effect.

