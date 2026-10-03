# 1.0.4-dev.17 integrated evidence — 2026-10-03

Baseline: 78c536688ab39f1ed062dee56690bfd29ce7c5a1 / dev16. This is a development build, not a public release or tag. Four reviewed changes have separately bounded measurements or behavioral proofs.

## Changes and limits

- The GUI model/orchestrator picker focuses its existing search input after the popup becomes visible. The hidden-input focus was ignored by a real browser, so typing immediately after opening did not filter. Owned native one/100-rebuild fixtures now focus the exact input, filter with real keyboard input, preserve HTML/save/copy behavior and retain the existing single-listener lifecycle. No extra query, listener, timer or persistent field is added. This is a concrete focus correction, not a WebView2 RSS/FPS benchmark. [Details](2026-10-03-goal-picker-visible-search-focus.md).
- GUI auto-memory preference lookup resolves current/legacy keys from one fresh settings snapshot instead of two eager reads/decodes. Public complete-loop preparation reduces transient allocation by approximately 11–13 KB for a 4,134-byte settings fixture and 54–61 KB for a 27,513-byte fixture. Full-loop time ranges overlap and the absent-file case has no demonstrated gain. The leaf lookup removes one read/decode, halving 164 allocations to 82. Fresh saved-setting/default/type/error behavior passes fourteen real Engine cases. No standing settings cache is added. [Details](2026-10-03-goal-ui-settings-snapshot.md).
- Structured OpenAI reasoning chooses its existing string/array/object decoder from the first non-whitespace byte, avoiding doomed decoder calls. Public Complete controls reduce transient allocation by about 64.6 KB / 98.4 KB per 128 structured object/array fragments. Recursive differential, generic/Zen outgoing wire, full Delta/native and error controls match. Timing is inconclusive: even the unchanged plain path was slower in the middle candidate arms; ordinary flat allocation is equivalent. Real structured-field incidence is unknown. This does not establish faster ordinary Qwen or higher model TPS. [Details](2026-10-03-goal-reasoning-shape-dispatch.md).
- A verified non-inert worker edit now expires the appropriate parent's stale command-failure gate, allowing the required identical check to run. This also works for explicit custom nested factories; built-in worker delegation permissions are unchanged. Public task/continuation/nested fixtures reproduce the original blocked reruns and pass after the fix. Outstanding failed-check evidence clears only after an actual passing check. Workspace identity, generation/session, cancellation, no-op/error/read-only and background controls remain. The common workerInvocation stays 32 B on 64-bit; optional delegation context/closure has unmeasured transient cost. Aliases require directory metadata, including case-only spellings on Windows; exact roots need no I/O. This enables necessary verification, not measured historical token/turn savings. [Details](2026-10-03-goal-worker-mutation-recovery.md).

No standing prompt, tool description, dependency, model request or provider-setting change was introduced. Zen request/Delta parity remains proven in the scoped controls. No live model POST, model reload, GPU workload or intervention in user applications occurred in this tranche. Synthetic fixtures and test/build/install evidence stay in portable repository subfolders.

## Integrated verification

- Full Go suite: 71 test-bearing packages passed; 28 packages have no tests.
- Frontend suite: 177 passed, zero failures/skips/cancellations; 893.8704 ms in the completed run.
- go vet ./... and the owned ten-Go-file gofmt gate passed without diagnostics. git diff --check passed with existing LF/CRLF notices only.
- All 1,844 cmd/internal/test plus module-file entries remain byte-identical through full checks, ten builds and installation. All 16 source/test/per-scope reports match owner freezes.
- Ten CGO-disabled builds passed: CLI/GUI for Windows amd64, Linux amd64/arm64 and macOS amd64/arm64. This is cross-build validation, not runtime validation on each target.
- Both installed Windows EXEs match the dev17 build manifest; CLI reports 1.0.4-dev.17, and CLI version/help plus GUI help pass. Atomic verified staging/rename has portable dev16 backups. No user application was killed, closed or restarted.

| Installed binary | Bytes | SHA-256 |
| --- | ---: | --- |
| supercli.exe |26,313,216|6234a351a03a84bb82cefe55a7b8ade50b0621f9458ef852b109f561fcfaa207|
| supercli-web.exe |22,990,336|313f568b1ed22fa9a03b0731d5389f99d4dd92e9d24f02ae2bc1c87ae75ee187|

Both binaries are 4,096 bytes larger than dev16; no binary-size reduction is claimed. The first root summary parser rejected Node's spec-reporter prefix; completed logs were parsed correctly without rerunning successful tests. Installation succeeded on its first execution after syntax checking.

## Remaining authorized work

The explicit GUI request to keep image-reading previews within expandable tool details and remember expansion preferences is being prepared separately; it is **not** implemented by this build. Restore needs explicit host-set image provenance, lazy preview materialization and provider/storage/page-boundary parity. Actual user attachments and explicit user-facing media must remain visible.

The next runtime candidate is eager registry.Names materialization for exact known-name hardening. It is ignored/static only; public correctness and a bounded measurement against post-dev17 are still required. A naive history-estimation early break was rejected on static overflow parity grounds and was not ported. Per-turn caches, lossy result shortening, worker unloading and broad raw/native protocol rewrites remain outside these approved changes. The optimization goal stays active while these concrete items remain.

Receipts and commands: ignored .tmp/goal-integrated-dev17-2026-10-03. Final commit/push source guard is stored there after publication.
