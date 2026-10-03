# Conditional tool string-length validation — 2026-10-03

Baseline: main 6ab0f1c / dev14. Scope: registered tool argument validation.

## Problem and change

The compiled validator counted every string's Unicode code points even when its schema supplied neither minLength nor maxLength. The count is now computed only when one of those constraints exists. Explicit zero limits still apply. Pattern, type, enum/const, object-field, required-field and branch checks retain their original order and behavior. Tool arguments and provider bodies are unchanged; there is no new cache, prompt, instruction, allocation or provider call.

## Matched evidence

Windows/amd64, Ryzen 7 5800X3D, Go 1.26.2 with portable caches, CGO_ENABLED=0. Baseline/candidate/candidate/baseline runs used 100 ms per case with no other CPU benchmarks or model calls. The existing public Registry.Execute benchmark measures registered argument preparation and validation with a minimal tool callback; it does not include real file writes, provider work or the full turn.

| Existing benchmark label / actual body bytes | Baseline | Candidate | Go allocations |
|---|---:|---:|---:|
| create_file_32k / 36400 B | 244–246 µs/op | 218–233 µs/op | approximately 211,786 B/op, 24 allocations in both |
| memory_note_4k / 4140 B | 28.0–29.5 µs/op | 25.4–26.1 µs/op | approximately 20,167 B/op, 30 allocations in both |

Numeric/argv controls overlap within noise. Separate generated typed-callback body cases were noisy, including a candidate 32 KB outlier slower than baseline; they do not establish a general speed claim. Allocated memory is unchanged. This removes an unnecessary linear scan, without claiming lower request tokens, fewer turns, faster native decode/prefill, process RAM or a measured end-to-end latency improvement.

## Verification

Both the original validator and the final tracked validator passed the complete core package with the new controls (0.448 s / 0.463 s); final go vet passed. New public Registry.Execute regressions preserve accepted raw JSON exactly, Unicode code-point limits including combining marks and emoji, explicit maxLength=0, and pattern-only checks on an unbounded string. Existing schema/argument tests remain intact.

Private portable receipts and complete ABBA logs: .tmp/goal-tool-args-round12-2026-10-03. No live model or user application was changed.
