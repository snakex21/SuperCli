# 1.0.4-dev.14 integrated optimization evidence — 2026-10-03

Baseline: 306ff569c4c6df05e667e9564d4e5ffa098b82d6 (1.0.4-dev.13). Four bounded changes are integrated and installed locally; this is a development build, not a new public release or tag.

## Changes and measured limits

- TUI reuses exact terminal widths of unchanged history lines instead of rescanning every old line on each refresh. The final 2,400-message whole-history refresh/View benchmark measured 7.672–7.932 ms before and 1.646–2.246 ms after. The immutable cache adds approximately 54–57 KB of transient metadata at this generated size; it is a CPU improvement, not a RAM saving. Rendering, scrolling, keyboard/mouse and renderer-command differential tests use the actual pinned upstream as the oracle. [Details](2026-10-03-goal-tui-line-width-reuse.md).
- GUI releases both remaining original tool-input references after the full formatted input has been successfully materialized. Folded inputs and failed formatting/insertion keep their existing behavior. Twenty generated large inputs released 3,195,296 B of V8 heap while full input hashes, node identity/order and visibility were preserved. This is explicit-GC ownership evidence, not native WebView2 RSS or a measured FPS increase. [Details](2026-10-03-goal-live-input-residency.md).
- Small fresh successful ctx_execute results omit a byte-identical 64–255-byte command echo only in the model-facing view, capped at 1,024 original result bytes. Complete UI/persisted evidence and failure/retention/legacy controls stay unchanged. A bounded historical sample qualified 246 results and 38,983 model JSON bytes; a synthetic real-serializer fixture removes 141 model JSON / 145 wire bytes. The extra copy is bounded to one small string. No native token count, prefill or decode throughput claim is made. [Details](2026-10-03-goal-short-command-echo.md).
- Session images are published from complete closed temporary files. Existing incomplete files are repaired, complete deduplicated files are not replaced on Windows, nonregular paths fail closed, and failed publication keeps the existing inline-pixel fallback. Actual concurrent writers, held-open files, cleanup and long portable paths are covered. A cold 1 MiB write measured approximately 0.41 ms additional cost; this is a reliability correction, not a performance win. Same-size external corruption, power-loss durability and simultaneous cross-process repair are outside the guarantee. [Details](2026-10-03-goal-session-image-publication.md).

No standing prompt/tool schema, provider/model setting, context/KV setting or OpenCode Zen routing change was introduced. The TUI component adds no module dependency; its upstream MIT notice is retained in the component and distribution root license. Native model decode throughput is separate from these harness measurements. The user reported that reloading Qwen restored approximately 36 tokens/s; this observation does not establish an engine root cause. One read-only local /v1/models request was unavailable during this round, so no live model POST, reload, settings or cache intervention was performed.

## Integrated verification

- Full Go suite: 71 test-bearing packages passed; 28 packages have no tests.
- Full frontend suite: 170 passed, zero failures/skips/cancellations; 941.1723 ms for this completed run.
- go vet ./... passed with no diagnostics. git diff --check passed; only existing LF/CRLF checkout notices were printed.
- Frozen source guard: all 1,831 cmd/internal/test plus module-file entries remained byte-identical across checks, ten builds and installation. The 25 scoped source/test/license/evidence files also match their owners' frozen hashes.
- Ten successful CGO-disabled builds: CLI and GUI for Windows amd64, Linux amd64/arm64, and macOS amd64/arm64. Cross-build success does not substitute for platform runtime testing.
- Local Windows CLI and GUI replaced through verified staging and rename with portable dev13 rollback copies. CLI reports 1.0.4-dev.14; CLI/GUI --help checks passed. No user application was killed, closed or restarted.

| Installed binary | Bytes | SHA-256 |
| --- | ---: | --- |
| supercli.exe | 26,288,640 | f162f9d81477123cad38c2d5b223451c3fd3a75188fb4e2eb7ff2c7069a5cfd7 |
| supercli-web.exe | 22,965,248 | 8a14cd6f18489d8ead8ca7902aa2f8812ee11b6814f4c639bf685db3db4bd35f |

The new files are approximately 43.5 KB / 6.7 KB larger than dev13. No binary-size reduction is claimed for this part. Exact platform artifact hashes, commands, complete test outputs, frozen source manifests, installation and rollback receipts are in portable ignored .tmp/goal-integrated-dev14-2026-10-03. Benchmark fixtures and profiler output remain in the corresponding per-scope portable evidence directories. No user conversation, pixels or private prompt was committed.
