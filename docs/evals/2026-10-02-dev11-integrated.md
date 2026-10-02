# Integrated dev11 validation — 2026-10-02

Base: b754435 (dev10). Installed local Windows version: 1.0.4-dev.11. Stable release metadata is unchanged; no release, tag or updater publication is part of this checkpoint. All new temporary, build, evidence and backup writes stay inside the portable repository.

## Changes and evidence

- Interrupted TUI background memory summaries no longer persist partial streamed text, advance turn coverage or mark an interrupted startup pass complete. Parent cancellation stops the pass; actual metered foreground preemption is distinguished from an ordinary provider error, preserving the once-per-startup behavior for failed providers. A canceled parent also returns when a provider never closes its stream. Deterministic user facts and successful summaries retain their previous behavior. Real metered fixtures reproduce the defects and check persistence, coverage, error identity, retry and call sinks. [Memory evaluation](2026-10-02-goal-memory-preemption.md).
- Loaded GUI history releases the redundant raw message array after a successful render and no longer concatenates already-rendered pages. DOM nodes and lazy expansion closures retain the full content, results and media. A generated 2,700-message fixture releases 2,700 message objects and 1,350 call wrappers, freeing about 928 KB in one V8 heap measurement and avoiding 62,040 redundant reference copies across 45 pages. This is not a native RSS, FPS or decode-rate measurement. [History evaluation](2026-10-02-goal-rendered-history-residency.md).
- Provider image projection now operates on the independently owned outgoing message slice instead of making another full history copy. Image-bearing parts still copy before modification, and leading system images, dormant markers, cached estimates and chat-window boundaries preserve the previous request. One saved 1,859-message replay reduces coordinator allocation from 1,719,734 to 1,506,737 bytes per sequence (12.4%); six replay records preserve wire bytes, hashes and token estimates exactly. Shared pruning and compaction pricing remain unchanged. CPU differences are small and do not explain native prefill or decode throughput. [Projection evaluation](2026-10-02-goal-owned-media-projection.md).
- ZIP extraction declares its explicit destination as a directory write as well as an archive read. Dependent reads and patches now wait for extraction; list operations and unrelated destinations retain their existing parallelism. Missing or invalid destinations use a conservative barrier. Real tool-loop fixtures reproduce premature reads and failed patches; the selected historical sessions had no ZIP calls, so no historical model-turn saving is claimed. [Dependency evaluation](2026-10-02-goal-zip-dependencies.md).
- ZIP extraction resolves entry paths and refuses unresolved link or junction boundaries before writing. Native Windows fixtures reproduce writes escaping through directory, file and dangling symlinks and junctions. Ordinary extraction and safely resolved links retain their output. This is a correctness fix with on-demand metadata checks, not a performance saving; it does not claim hostile TOCTOU protection, hard-link isolation or transactional rollback of earlier safe entries. [Link-boundary evaluation](2026-10-02-goal-zip-link-boundary.md).

## Qwen status and limits

The user reports that unloading and reloading Qwen restored approximately 36 tok/s after the earlier 27 tok/s period. This round did not independently time that recovery or change the model, engine, KV cache, context or load settings. The observation strengthens the loaded-runtime-state explanation: the prior round replayed an archived text-only greeting with unchanged content and three tool contracts and still measured 28.05 tok/s against its historical 36.19. No exact engine or GPU failure mechanism is established.

This round uses CPU fixtures and differential request replays, not model calls. It sends no additional prompt instructions or tool schemas, reduces no request tokens by lossy omission and makes no promise to improve Qwen decoding. The special OpenCode Zen path is unchanged. The office-reader ReadOnly proposal was rejected because the existing scheduler already accounts for those read footprints; no measured parallelism benefit justified it.

## Verification

Full go test ./... -count=1 passed all 70 test-bearing packages; 28 other packages have no tests. Full go vet ./... and git diff --check passed. All 162 frontend tests passed on the final unchanged JavaScript sources; their receipt and frozen hashes were verified during integration. Scoped app, memory, metering, agent and office regressions passed before integration. A fresh 1,813-file guard covering cmd, internal, test, go.mod and go.sum remained unchanged through the full checks, cross-builds and installation.

Ten binaries compiled: CLI and web GUI for Windows amd64, Linux amd64/arm64 and macOS amd64/arm64. Foreign builds were not executed. Windows binaries were atomically installed with the previous EXEs backed up inside .tmp. Installed CLI --version and both --help commands passed, and installed hashes match the built artifacts.

| Windows artifact | Bytes | SHA-256 |
| --- | ---: | --- |
| supercli.exe | 26232832 | c49e01aae761f25ffa178bcf555e8f0907a93ed2e5cf59b1b231b74a18a444ad |
| supercli-web.exe | 22957056 | b06352cd5f7c8ab872da62ee1f4248a9b57cd18732808b733cdd0120fcee1e55 |

Ignored integration receipts are under .tmp/goal-integrated-dev11-2026-10-02. No private prompt, session ID, pixels, credentials or raw LM Studio log enters tracked reports. Restart an already-running GUI to load the updated binary and embedded JavaScript. The broader optimization goal remains active; future changes require a reproduced defect or a measured benefit.
