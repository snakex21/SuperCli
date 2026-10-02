# Integrated dev10 validation — 2026-10-02

Base: ab2ebb9 (dev9). Installed local Windows version: 1.0.4-dev.10. Stable release metadata is unchanged; no release/tag/updater publication is part of this checkpoint. All new cache, temporary, build, evidence and backup writes stay inside the portable repository.

## Changes and evidence

- Requested contracts embed full JSON schemas without quoting their JSON twice; malformed or legacy schemas keep their exact previous rendering. The stable thin coordinator dispatcher omits only eligible signatures duplicated by the full definitions actually sent. In one serial, fully cold Qwen desktop pair, input fell from 5,187 to 5,026 actual tokens (3.10%), both selecting and validating the correct desktop tool without another model turn. Native prefill improved by only 129 ms in this single pair, while both decoded at 28.10 tok/s. [Contracts and measured limits](2026-10-02-goal-lossless-tool-contracts.md).
- Folded live tool inputs now format on expansion, preserving raw arguments, task briefs, input/output order and media previews. A generated replay matching saved aggregate sizes avoids 1,193 prettyJSON calls and 4,772 hidden DOM nodes. Expanded content is identical; no native RSS or FPS claim is made. [Frontend evaluation](2026-10-02-goal-live-tool-input.md).
- Explicit visual folder indexing now yields to foreground chat through the existing metering gate. A genuinely preempted image discards partial text and retries its original payload under the same 90-second deadline. Ordinary errors do not retry, and canceled jobs do not cache a partial caption or stamp successful completion. The metering early-error path snapshots cancellation before its own cleanup, fixing false Canceled flags. Real coordinator fixtures reproduce both prior defects and verify sinks, cache, retries and cancellation. [Scheduling evaluation](2026-10-02-goal-visual-index-preemption.md).

## Qwen regression and rejected work

Replaying the archived 1,683-token, three-tool greeting with the same content and contracts, no pixels and no capture execution measured 28.05 tok/s instead of the historical 36.19. The output cap was the only changed wire parameter. This confirms native throughput degradation without attributing it to newly added screenshot schemas or GUI rendering. Completed logs show no model reload or setting change across the first observed decline. GPU residency/clocks/temperature telemetry is absent, so its exact cause remains unresolved; this checkpoint does not claim to restore 35–36 tok/s. The separate second greeting had warm-cache and reasoning-control differences and was excluded from causal comparison.

A matched same-response read_lines grouping fixture saved temporary allocations but was slower (19.5% for its short-line corpus), so no automatic batching/cache was ported. An ask_user $ref rewrite weakened the current local validator and was rejected. No provider-specific route, model/KV/context setting, special Zen path, registered schema or execution permission was changed.

## Verification

Full go test ./... -count=1 passed all 70 test-bearing packages; 28 other packages have no tests. Full go vet ./... and git diff --check passed. All 161 frontend tests passed on the final unchanged JS sources. Scoped agent, metering and web GUI regressions and vet passed before integration. A fresh 1,842-file source/hash guard remained unchanged through the full checks, cross-builds and installation.

Ten binaries compiled: CLI and web GUI for Windows amd64, Linux amd64/arm64 and macOS amd64/arm64. Foreign builds were not executed. Windows binaries were atomically installed with previous EXEs backed up inside .tmp. Installed CLI --version and both --help commands passed, and installed hashes match the built artifacts.

| Windows artifact | Bytes | SHA-256 |
| --- | ---: | --- |
| supercli.exe | 26223616 | c89cb4d3e665ea7547f1741f0f7f93ebeb875e88e449afc73a18bea3be5e2ad6 |
| supercli-web.exe | 22951936 | 239ed79af33741567c41c36f94885b4abc2b9ce560848c049fdc9ea92cd479a6 |

Ignored integration receipts are under .tmp/goal-integrated-dev10-2026-10-02. No private prompt, session ID, pixels, credentials or raw LM Studio log enters tracked reports. The broader optimization goal remains active.
