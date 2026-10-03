# 1.0.4-dev.16 integrated optimization evidence — 2026-10-03

Baseline: f58017476006cec9eeb52a67478e503f6a1f4beb (dev15). Four bounded changes; a development build, not a new public release or tag. Per-scope measurements compare each change against its own matched control, rather than adding their savings into an application-wide claim.

## Changes and measured limits

- The GUI orchestrator picker uses one named outside-click handler, registered at first use and deduplicated by EventTarget. It no longer retains every retired picker through a document listener. A synthetic native browser fixture after 100 fully materialized rebuilds retains 1 wrapper instead of 100, including release of the first retired wrapper. Native JavaScript heap is approximately 194 KiB lower. Stable public-click median is approximately 49 → 2.1 µs; a fresh single-picker control remains near 2 µs. The generic class query costs approximately 42 µs extra per click immediately after unrelated DOM changes in the fresh single-picker fixture. Arbitrary hosts, multiple connected pickers, filtering, saved values, labels, code copying and HTML agree. These are synthetic DOM/JS results, not WebView2 application RSS/FPS. [Details](2026-10-03-goal-picker-listener-residency.md).
- Tool footprint scheduling rejects unsupported resource names before parsing their arguments. Registered tools still pass the existing dispatch hardening, validation, verification and result handling; unknown footprints retain the existing sequential barrier. Small reachable-path fixtures remove approximately 1.2–3.4 KB of transient Go allocation and 3–9 µs per controlled batch. Session metadata shows potential exposure, not historical proof of every scheduling call. No model tokens or turns are removed by this change. [Details](2026-10-03-goal-tool-schedule-name-gate.md).
- Native Anthropic request replay uses the already-owned encoded bytes when establishing signed-history metadata. Foreign/custom readers retain the original read/close/error behavior; each retry captures its own body. Public Complete fake-transport controls preserve exact wire bytes, signatures, negotiated image fallback, redirect and retry behavior. At a nominal 1 MiB synthetic text input, transient Go allocation falls by about 3.3 MB and timings measure 37.80–39.15 → 36.09–37.28 ms. Tiny input has one additional small allocation, lower B/op and overlapping time; no universal latency win is claimed. Zen retains its original GetBody route. [Details](2026-10-03-goal-anthropic-owned-body-replay.md).
- The native Anthropic SSE parser keeps the first owned scanner string directly, allocating a builder only for multi-line data. Public Complete single-line stream fixtures remove approximately 12 KB / 311 KB / 590 KB of transient allocation. Small and 4 KiB chunk timings overlap; multi-line parsing is unchanged. Differential event/error and retained-string tests preserve complete data and ownership. OpenAI/Qwen and Codex/Zen use separate scanners. [Details](2026-10-03-goal-sse-single-line-copy.md).

No standing prompt, tool description, provider request, dependency, per-turn cache or startup query was added. The click listener appears only on the first picker use. No live model POST, provider/model reload, cache/KV/context settings change or intervention in user applications occurred during this tranche. The UI evidence uses only an owned hidden synthetic browser fixture with profile/cache/temp under the repository; it completed and closed its context. These changes do not establish higher model generation TPS, shorter GPU prefill, lower application-wide RAM or fewer agent turns.

## Integrated verification

- Full Go suite: 71 test-bearing packages passed; 28 packages have no tests.
- Full frontend suite: 176 passed, zero failures, skips or cancellations (1,017.8666 ms in this completed run).
- go vet ./... passed without diagnostics. git diff --check passed; existing LF/CRLF notices only.
- All 1,839 cmd/internal/test plus module-file entries stayed byte-identical across tests, ten builds and installation. All 13 source/test/per-scope evidence files match their owners' frozen hashes.
- Ten CGO-disabled builds passed: CLI and GUI for Windows amd64, Linux amd64/arm64 and macOS amd64/arm64. Cross-build success does not substitute for platform runtime tests.
- Both local Windows EXE files were replaced through verified staging and rename with portable dev15 backups. CLI reports 1.0.4-dev.16; CLI and GUI --help passed. No user application was killed, closed or restarted.

| Installed binary | Bytes | SHA-256 |
| --- | ---: | --- |
| supercli.exe | 26,309,120 | 8060b6174f3dddf1432d36e9a25334962398b667ffea4e72f0a63ab5c336ebab |
| supercli-web.exe | 22,986,240 | c6ef01c4260d80dfade2fd1411a48b37d46915aaa4e1e1e5a8e0479ecf3fd575 |

CLI is 5,120 bytes and GUI 5,632 bytes larger than dev15; no binary-size reduction is claimed. Commands, logs, artifact hashes, frozen guards and installation receipts remain under ignored portable .tmp/goal-integrated-dev16-2026-10-03. The first installation runner had a syntax error before any execution; its corrected version was syntax-checked, installed both EXEs and passed the final verification. Synthetic fixtures contain no user conversations, prompts or pixels.
