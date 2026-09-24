# Allocate the visible history, not the hidden transcript — 2026-09-24

## Change and scope

VisibleMessages previously reserved a message buffer as large as the complete canonical transcript whenever hidden flags existed. After hiding a long prefix, a view containing ten visible messages and one placeholder could therefore allocate space for thousands of messages. The function is used by request assembly, token estimation and projection persistence.

The new code first counts visible messages and contiguous hidden runs using only the boolean flags, then reserves the required capacity. The rendering pass accesses a message only when it is visible. Placeholder text, chronology, message Parts, tool calls/results, hidden flags and canonical history are unchanged. The unhidden fast path still returns the existing read-only view with zero allocations. No cache, prompt, model call, schema, provider transport or persistent format is added.

This helps histories with hidden entries, such as after clearing older turns or budget-driven hiding. It does not claim the same saving for every form of compaction: a conversation already replaced by a short summary may have no hidden transcript. Full transcript storage and the model's token count are unchanged. The defect was found by inspecting the current code, not by claiming a production-session latency measurement.

## Measurement

Windows / Ryzen 7 5800X3D. Four samples per variant, 200 iterations per sample, ordered before/after/after/before. The baseline overlay replaces only context_hide.go. Numbers below are medians of local CPU benchmarks; allocated bytes are temporary allocation per operation, not total process RAM.

| Operation / fixture | Before | After | Allocated bytes before → after |
| --- | --- | --- | --- |
| Visible view, 1000 messages / last 10 visible | 16.015 µs | 1.476 µs | 114907 → 1352 |
| Visible view, 10000 messages / last 10 visible | 91.849 µs | 11.031 µs | 1122807 → 1352 |
| Visible view, groups of 8 hidden / 2 visible | 21.300 µs | 15.583 µs | 121187 → 47400 |
| Visible view, alternating hidden/visible | 59.909 µs | 63.339 µs | 146816 → 146826 |
| Visible view, allocated all-false flags | 16.188 µs | 20.513 µs | 114688 → 114689 |
| Visible view, no hidden flags | ~3 ns | ~3 ns | 0 → 0 |
| Request preparation, native tools | 96.928 µs | 36.167 µs | 600806 → 33792 |
| Request preparation, thin tools | 92.371 µs | 29.164 µs | 607317 → 40347 |

The request-preparation fixture contains about 1000 messages with the old prefix hidden. Each iteration performs the two pre-request token estimates and then assembles/estimates the outgoing request with its tools, matching the existing preparation benchmark's structure. It excludes network traffic, inference, database writes and GUI rendering.

The extra flags pass has a cost: alternating single-message hidden runs and all-false flags showed roughly 3.4 and 4.3 µs increases, respectively, without allocation savings. The usual nil-flags path is untouched. Timing samples are short and noisy; small differences should not be extrapolated to end-to-end latency. Large-prefix allocation reductions are the main result. No live Qwen/cloud run was needed or performed for this content-preserving change, and there is no provider token or inference speedup claim.

## Compatibility and validation

- Exhaustive masks over a six-message fixture, including shorter/longer flag arrays, match the prior rendering logic. The fixture includes images, Parts and a tool call/result pair.
- Empty/nil histories, contiguous/alternating runs, appends, additional hiding and reset retain their behavior.
- Canonical history remains unchanged, and previously returned views remain valid after later hiding.
- Prepared native and thin message lists match the reference view and produce identical estimates. Before/after JSON hashes match after normalizing only the generated request timestamp; the timestamp itself remains unchanged in production.
- Native fixture: 3626 normalized JSON bytes, SHA-256 82671859f4d36c01abff8ddbbbf5e67ce20a8e11f29a9e42c664389aabbf8712, request estimate 5867.
- Thin fixture: 9857 normalized JSON bytes, SHA-256 f63f73b82f2bf91a5d6fe55550105bf4b1500278938ae63a6b15a1a3f2c1d5b5, request estimate 3498.
- Existing hide, budget eviction, model handoff, media and projection persistence tests pass.
- Full go test -timeout=90s ./... and go vet ./... passed.

Artifacts in .tmp/visible-history-buffer/ include the original source/overlay, before/after compatibility checks, raw benchmark samples and medians, normalized request comparison, full checks, source diff, release builds, smoke checks and installation verification. OpenCode Zen's special path was not modified.
