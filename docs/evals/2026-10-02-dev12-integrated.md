# Integrated dev12 validation — 2026-10-02

Started 2026-10-02; finalized 2026-10-03 (Europe/Warsaw). Base: 2b4c132 (dev11). Installed local Windows version: 1.0.4-dev.12. Stable release metadata is unchanged; this checkpoint publishes no release, tag or updater manifest. All new test, build, evidence, cache and backup writes remain inside the portable repository.

## Changes and evidence

- File-backed image data URIs now encode into one pre-sized output builder instead of retaining intermediate full-size base64 buffers. URL and inline Data paths, reads, errors, image refs and provider behavior are preserved. Twelve full OpenAI/Responses/Anthropic request bodies match the baseline bytes and hashes. At 699,020 image bytes, the synthetic full request allocates 4,589,740 → 3,113,825 B/op, about 1.48 MB less (32%). Small-request CPU time does not improve, and these are allocations rather than native RSS, inference tokens or decode speed. [Encoding evaluation](2026-10-02-goal-file-image-encoding.md).
- The worker overview places an existing button only when its sorted position changes. Labels, states, summary, button identity and task-row click behavior remain identical at every generated event. Four workers and 171 overview events make 678 → 3 existing-button moves; sixteen fixture workers make 10,680 → 15. This measures removed DOM operations, not live layout/FPS or configured worker limits. [Overview evaluation](2026-10-02-goal-worker-overview-dom.md).
- TUI background memory timers now invalidate expired callbacks on Activity, replacement Schedule and Close. The real AfterFunc overlay reproduces an unwanted production summarizer call after Activity and after rescheduling; both go from one stub Complete call to zero, while the ordinary callback still works. Separate job ownership preserves cancellation when an older canceled job finishes late. The unchanged 15-second delay adds no timer or inference. Callback capture costs eight additional allocated bytes per arm; CPU timing overlaps. [Timer evaluation](2026-10-02-goal-idle-timer-generation.md).

No model prompt, tool contract, pixel content or special OpenCode Zen route was changed. The lossless changes reduce application work without reducing the information available to an agent. No historical saved-task turn reduction or live RAM saving is inferred from these component fixtures.

Neighboring harnesses mostly repeat the existing lazy skill/resource and evidence-reuse mechanisms. A reachable duplicate cold skill-body read saves only 58–438 µs in a four-caller fixture, with zero observed apply_skill calls in the bounded saved sample; its lifecycle machinery is deferred. [Audited candidate and limits](2026-10-02-goal-cold-skill-audit.md).

## Independent Qwen observation

A read-only snapshot of completed LM Studio records after the user's model reload shows native decoding around 34–35 tok/s and substantially higher prefill throughput than the earlier degraded period. These are different completed requests, not a controlled same-input A/B. Client TTFT is available in the saved call records; exact request wire hashes and provider-reported cache counts are unavailable. No extra model POST, warmup, retry, engine setting, cache reset or model reload was performed for this observation. It supports the user's report of recovery while leaving the exact cause unproven; it is not a gain attributed to this patch. [Bounded observation](2026-10-02-goal-qwen-reload-observation.md).

## Verification

Full go test ./... -count=1 passed all 70 test-bearing packages; 28 other packages have no tests. Full go vet ./... and git diff --check passed. All 164 frontend tests passed on the final combined source. Scoped llm and app tests/vet, worker-overview differential fixtures and an independent timer review passed before integration. A fresh 1,816-file guard covering cmd, internal, test, go.mod and go.sum remained unchanged through full checks, builds and installation.

Ten binaries compiled: CLI and web GUI for Windows amd64, Linux amd64/arm64 and macOS amd64/arm64. Foreign binaries were not executed. Windows EXEs were atomically installed with previous files backed up inside .tmp. Installed CLI --version and both --help commands passed; installed hashes match their built artifacts.

| Windows artifact | Bytes | SHA-256 |
| --- | ---: | --- |
| supercli.exe | 26234880 | 8c24ef0137b60ee4cc92c7f9d068743e8b6d38bdb817e1aab06c1601bd8eaa99 |
| supercli-web.exe | 22958592 | 2fb60f4839067f3ff8f50df39ab00fc0b0c0e3fe4c280820f720fcfc3ccc90f6 |

Ignored integration receipts are under .tmp/goal-integrated-dev12-2026-10-02. No private prompts, session identifiers, image pixels, credentials or raw native logs enter tracked reports. An already-running GUI needs a restart to load dev12 and its embedded JavaScript. The broader optimization goal remains active; each further production change needs a reproduced defect or measured benefit.
