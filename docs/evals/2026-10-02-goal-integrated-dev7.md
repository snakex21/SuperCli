# Integrated SuperCli dev.7 — 2026-10-02

The user reports that Space Bunny continues generating while its visible speed varies in the GUI. A bounded live provider trace found an 862-ms interval before a response-body read completed, followed by catch-up; the backend consumed that trace without buffering any of its 14 text events. Separately, deterministic frontend replay proved that a remembered long gap could keep already received text on a roughly 332-ms display horizon. Clearing only cadence history beyond 600 ms reduces that resumed packet horizon to 40–60 ms, with the first fragment still on the next animation frame. This is received-text display recovery, not an inference/TTFT improvement or an elimination of upstream silence.

## Integration

Merge 24f0147 combines local efficiency commit 4614dc1 with remote media/headless/control commit 9b9b43f. The merged handler retains direct byte SSE framing, one flush per event and session-scoped image-preview handles. The TUI retains its direct key dispatcher and draft recovery; a new public-update regression covers all 16 confirmation scenarios including both input batch types, Ctrl+C with a queue and resize.

Image verification now validates both a present primary image and every additional image, preventing a valid additional image from masking an empty primary. The optional fal video adapter now waits on one status SSE connection instead of repeated GETs. Its local three-state fixture makes four requests rather than six, with one submission, no reconnect/re-POST and bounded cancellation/output. Media tools remain conditional and no media provider was invoked during this integration.

## Validation and installed artifacts

- Full integrated `go test ./... -count=1 -p 2`: PASS, 70 tested packages.
- Full integrated `go vet -p 2 ./...`: PASS.
- All 154 JavaScript UI tests: PASS on the merged pacing source; its SHA guard matches the tested source. The three new pacing tests fail on the baseline overlay.
- 1819 source/embedded-resource files remained byte-identical across the final Go checks and all builds.
- Ten CGO-disabled builds: TUI and GUI for Windows amd64, Linux amd64/arm64 and macOS amd64/arm64. All PASS. Cross-compilation does not imply runtime validation on those other OSes.
- Installed Windows EXEs: 1.0.4-dev.7. Backups and SHA256/build/check records stay under portable `.tmp/goal-integrated-dev7-2026-10-02`. No stable release/tag/updater manifest is published by this round.

The earlier round-four benchmarks remain component measurements: about half the bytes allocated by ordinary TUI key updates and by large SSE frames. They do not measure process RSS or end-to-end task speed. No claim is made that WebView2 startup RAM or physical GUI/TUI FPS improved in this integration. There are no new dependencies or prompt instructions, and the special Zen provider path is unchanged.

Reports: [live provider cadence](2026-10-02-space-bunny-cadence.md), [GUI recovery](2026-10-02-goal-cloud-cadence-recovery.md), [image verification](2026-10-02-media-verifier-integration.md), [fal status stream](2026-10-02-fal-video-status-stream.md), [round-four efficiency](2026-10-02-efficiency-goal-round-four.md).
