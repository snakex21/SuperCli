# Use available space for uneven batch reads — 2026-09-24

## Problem and change

read_many split its display budget equally among all requested items. With many tiny files and one moderate file, that file could lose its middle even when the complete captured batch fit the existing 12 KiB model inline budget. The resulting retained-output handle then required another lookup to recover evidence already read.

The renderer now returns the complete captured batch when it has multiple successful items, each body is at most the existing 8 KiB item cap, and the entire result including headers, newlines and summary fits 12 KiB. Larger batches, oversized items, errors and single-item results keep their previous policy. File-reading limits and per-line truncation remain intact: complete captured output does not imply that every requested file was read to EOF.

The code uses the retained string already assembled during rendering. It adds no instruction, tool schema, classifier inference, cache or provider-specific behavior. OpenCode Zen transport is unchanged. The motivating defect was reproduced from code inspection in a synthetic fixture, not attributed to a newly observed production session.

## Controlled live comparison

scripts/uneven-read-batch/main.go executes a fixed initial read_many over twelve synthetic files, then passes its actual model-visible result to the real agent loop. Only read-only retrieval tools are exposed. The middle case asks for BudgetLimit=6842 on line 55 of file5.env; eleven sibling files are tiny. The head control asks for InitialLimit=1739 on line 3 of the same fixture, already visible before the change. The balanced control has small equal-sized files and already fits inline.

| Qwen case | Model calls before → after | Extra tool calls | Seconds before → after | Input tokens before → after | Initial result bytes |
| --- | --- | --- | --- | --- | --- |
| Middle evidence | 2 → 1 | 1 → 0 | 12.984 → 5.905 | 6202 → 4314 | 3687 → 6838 |
| Head-only control | 1 → 1 | 0 → 0 | 2.982 → 3.180 | 2870 → 4314 | 3687 → 6838 |
| Balanced control | 1 → 1 | 0 → 0 | 5.755 → 2.673 | 4025 → 4025 | 6416 → 6416 |

All six answers were correct. The middle baseline searched its saved output for BudgetLimit; the candidate needed no follow-up retrieval. Total input in that case fell by 1888 tokens, about 30%. The head control shows the tradeoff: 1444 extra input tokens and no call saved, with a slightly longer measured response. This is not a universal token or latency reduction.

The balanced request has identical content and size before/after, so its latency difference cannot be attributed to this change. Model timing and cache state vary. Each case has one paired run, with all baseline cases followed by all candidate cases. Timing starts after the fixed initial tool operation and excludes initial tool selection; this is a continuation experiment, not an unconstrained project task. Input totals include cached tokens and are not billing estimates.

Model: qwen3.8-27b-uncensored through LM Studio at 127.0.0.1:1234, reasoning requested at low. No live cloud comparison was run for this change. The implementation is shared across providers, and automated tests cover both native tool calls and thin/sentinel tool calls; these checks do not establish cloud latency gains.

## Local rendering cost

Windows / Ryzen 7 5800X3D. Four samples per variant, 1000 iterations each, in before/after/after/before order. These benchmarks render in-memory results and exclude file reads, persistence I/O and inference.

| Fixture | Median before | Median after | Allocations/op |
| --- | --- | --- | --- |
| Uneven | 6.591 µs | 6.816 µs | 40 → 40 |
| Balanced | 7.486 µs | 7.278 µs | 41 → 41 |
| Large | 88.306 µs | 86.647 µs | 175 → 175 |
| Failed | 6.703 µs | 6.059 µs | 41 → 41 |

Samples are noisy and allocation counts are unchanged. No local CPU speedup is claimed. The intended benefit is avoiding a model retrieval round when a moderate result was unnecessarily cut by equal per-item shares. Newly eligible results also no longer need a retained-output handle; persistence timing was not measured.

## Validation

- The real twelve-file read regression fails against the original renderer and passes after the change.
- Boundary tests cover complete result sizes of 12287, 12288 and 12289 bytes, including metadata, and item sizes of 8192 and 8193 bytes.
- Error visibility, failed-batch retention and single-item preview behavior remain covered.
- Agent-loop tests verify that the exact complete result, including its middle evidence, reaches both native and thin/sentinel protocols.
- Full go test -timeout=90s ./... and go vet ./... passed.

Artifacts under .tmp/uneven-read-batch/ include the original source and overlay, expected regression failure, targeted/full checks, live runs and comparison, benchmark samples, source diff, release builds/smokes and installation verification.
