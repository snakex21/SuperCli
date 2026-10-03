# 1.0.4-dev.15 integrated optimization evidence — 2026-10-03

Baseline: 6ab0f1cb20e6297444cf2378d678f5fa43917cbc (dev14). Four bounded changes; development build, not a new public release or tag.

## Changes and measured limits

- GUI drops inactive codeText/codeLang after a closed code block and in existing recovery/reset paths. Twenty generated large blocks release 2,709,024–2,709,184 B of V8 heap; a smaller fixture releases 27,920 B. Complete HTML/source, active nodes, later code, error recovery and copying stay unchanged. This is explicit-GC ownership evidence, not native WebView2 RSS/FPS. [Details](2026-10-03-goal-retired-code-residency.md).
- Saved tool outputs no longer require a second Go byte copy on UTF-8 databases. Encoding is checked once, lazily inside the first successful save transaction; UTF-16, unknown encoding and lookup failure keep the original byte binding. Public 1 MiB output pipeline median Go allocation falls from 1,208,486 to 158,785 B/op. Constant Store metadata grows by 16 bytes, with about 19 small first-save query allocations. Complete BLOB bytes, reference resolution, existing size limits and error priority remain. SQLite latency is noisy; no general latency/RSS/TPS claim. [Details](2026-10-03-goal-tool-output-binding.md).
- Compiled schema validation counts Unicode code points only when minLength/maxLength exists. The existing public argument-preparation benchmark with a 36,400-byte generated body measures 244–246 → 218–233 µs/op, with allocations unchanged. Pattern, type, enum/const, required and branch validation remains; explicit zero length limits still apply. This is a small removed scan, not a model-speed or token reduction. [Details](2026-10-03-goal-schema-string-length.md).
- Ordinary standard Responses requests are assembled before final serialization, avoiding a full encode/decode/re-encode of ordinary text. Public Complete with synthetic terminal SSE and two nominal 1 MiB text items measures 45.94–53.40 → 6.42–6.91 ms and approximately 33.4–39.7 → 8.5–9.1 MB transient Go B/op. Exact wire bytes, opaque/native numeric semantics, invalid UTF-8 fallback and error stages stay identical. The itemwise prototype was rejected because public-path allocations/time did not improve. Zen, subscription Codex and Chat Completions/Qwen routing remain unchanged. [Details](2026-10-03-goal-standard-responses-assembly.md).

No additional standing prompt, tool description, model call, dependency, per-turn cache or startup query was introduced. The SQLite encoding decision is lazy constant metadata. These measurements do not establish higher native model token throughput or fewer turns. The user reported reloading Qwen restored approximately 36 tokens/s; this suggests engine state mattered but does not prove its root cause. No live model POST, cache reload, provider/settings change or intervention in user applications was performed for this round.

## Integrated verification

- Full Go suite: 71 test-bearing packages passed; 28 packages have no tests.
- Full frontend suite: 173 passed, zero failures/skips/cancellations; 955.3239 ms for this completed run.
- go vet ./... passed with no diagnostics. git diff --check passed; existing checkout LF/CRLF notices only.
- All 1,834 cmd/internal/test plus module-file source entries stayed byte-identical across checks, ten builds and installation. The 15 source/test/evidence files match their owners' frozen hashes.
- Ten successful CGO-disabled builds: CLI and GUI for Windows amd64, Linux amd64/arm64, and macOS amd64/arm64. Cross-build success does not substitute for platform runtime tests.
- Local Windows CLI/GUI replaced via verified staging and rename, with portable dev14 backups. CLI reports 1.0.4-dev.15; CLI/GUI --help passed. No user program was killed, closed or restarted.

| Installed binary | Bytes | SHA-256 |
| --- | ---: | --- |
| supercli.exe | 26,304,000 | 8fcc42f68cf986e564fb7d0390fbca451da26ed457a218db694d4970796da69f |
| supercli-web.exe | 22,980,608 | 5c97b4c55d05aea0ff2f9cbd29bd6619fb530041aad6f30f7945a9e910f44ca2 |

Both installed files are 15,360 bytes larger than dev14. No binary-size reduction is claimed. Complete commands, test logs, platform artifact hashes, frozen source guards and installation/rollback receipts are in ignored portable .tmp/goal-integrated-dev15-2026-10-03. Per-scope private fixtures and measurements remain in their own portable directories. No user conversation, pixels or private prompt was committed.
