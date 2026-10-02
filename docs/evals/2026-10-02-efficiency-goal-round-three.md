# Efficiency goal — third measured round

Date: 2026-10-02. Baseline: main `cb7f507`. Goal remains active; this is a verified milestone.

## Shipped changes

- Repeated identical TUI viewport updates reuse the existing content instead of repeating Bubbles line splitting and width scanning. Idle rendering returns the existing completed transcript rather than building an identical copy. Width, height, welcome, language, scroll and changing-text behavior remain on their existing paths.
- Closing transcript search now applies a folded block immediately, also through a parent menu. A dirty empty chat preserves the welcome screen when confirming reasoning settings. Search target positions use the same ANSI wrapping and conversational separator as the transcript.
- Chat-native reasoning request construction reuses already validated decoded text for a provably canonical single-field payload. Noncanonical forms retain legacy typed-map decoding, including the duplicate-key type-error edge. Original payload bytes, signatures, scope filtering and validation errors are preserved.

No prompt, model request, timer, service, dependency or standing context was added. Input/output token counts and agent turns are unchanged. Portable storage and the special OpenCode Zen path are unchanged.

## Measurements and limits

Matched-source ABBA benchmarks, Windows amd64, Ryzen 7 5800X3D, Go 1.26.2, GOMAXPROCS=2:

| Repeated unchanged TUI history | Before | After | Transient allocated bytes before → after |
| --- | ---: | ---: | ---: |
| 500 mixed messages | 282.493 µs | 7.630 µs | 98,304–98,305 → 0 B/op |
| 50 messages | 35.347 µs | 9.237 µs | 9,472 → 0 B/op |
| Empty history | 7.576 µs | 7.934 µs | 16 → 0 B/op |

There is no claimed empty-history timing improvement. Model size increased by 24 bytes of string-header/flag/alignment state; LF content shares already owned viewport storage. CRLF input uses the original SetContent path without retaining an extra raw transcript key. A changed-content control benchmark (append and same-length replacement, 0/500 messages) retained the same allocation counts (145/136), with overlapping timing samples and no clear improvement or material regression. This does not measure native terminal FPS, input latency, process RSS or WebView2 memory.

The native-reasoning fixture measures validation, endpoint/model projection and the real JSON request builder, excluding HTTP and model generation. Eight canonical chat-native blocks totaling 4/64/512 KiB prepared 17–21% faster with 15–18% fewer allocated bytes. Requests without native chat replay have no claimed gain. No live Qwen/Zen/GPU experiment was performed this round.

Details: [TUI viewport and search](2026-10-02-goal-tui-viewport-search.md), [native replay preparation](2026-10-02-goal-native-reasoning-replay.md).

## Rejected and pending candidates

A completed-prefix cache had attractive append-batch measurements but failed exact parity under shared copied-history mutations and dynamic Style.Transform callbacks. It was rejected, and its speed figures are documented only as an unsafe upper bound.

A request-preparation scratch counter showed repeated immutable-text counting overhead. Its initial shared Begin/End lifecycle is not accepted: operation ownership, concurrent workers and all auxiliary provider calls must be isolated before considering integration. The revised operation-owned prototype passed independent-operation tests but saves about 50 µs at a cost of ~3.25 KiB transient allocation, one extra allocation even for plain history, a slower single cold pass and plumbing through ten candidate files. That tradeoff was rejected for integration. The experiment remains read-only in ignored .tmp; its figures are not shipped benefits.

## Verification and local builds

- `go test ./... -count=1 -p 2`: PASS.
- `go vet ./...`: PASS.
- `node scripts/test-ui.cjs`: all 136 PASS.
- Strong regression coverage includes manual scroll, viewport dimensions, model copies, welcome replacement, folded/expanded ANSI/Unicode search, 49 validation contracts, 960 generated native replay variants, local HTTP wire equality and unchanged canonical history.
- The full-source SHA guard stayed unchanged through tests and builds.
- GUI and TUI CGO=0 builds passed for Windows amd64, Linux amd64/arm64 and macOS amd64/arm64. Cross-compilation is not native-runtime verification.
- Local Windows EXEs installed as `1.0.4-dev.5` with matching SHA-256; CLI version and both help commands passed. Previous EXEs remain in the portable ignored round backup.
- The human Linux tester separately confirmed the previously provided dev.2 package works. This is not an end-to-end test of every newly cross-compiled target.

Windows build SHA-256:

| Binary | Bytes | SHA-256 |
| --- | ---: | --- |
| supercli.exe | 26,028,544 | 0cdb34478d704c398a6649b22d47c2cfce81813788042d85f628c914b7477d79 |
| supercli-web.exe | 22,746,624 | 342808abddc7ff7f5f5b5efc7c5f3aa5b5de2cb5678f544c20987e0d03c6b56f |

Raw check/build/benchmark/install records are in `.tmp/goal-round-three-2026-10-02`. No stable release, tag or update manifest was published.
