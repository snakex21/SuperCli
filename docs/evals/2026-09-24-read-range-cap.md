# Bounded reads for oversized ranges — 2026-09-24

## Reproduced problem

In the previous EOF experiment, Muse called read_lines with from=21 and to=521. Inclusive endpoints make this 501 lines, exceeding the 500-line limit. The tool returned only an error; the model then repeated the request with to=520. A regression over a real 25-line file reproduces the same rejection even though the actual available content is tiny.

read_lines now caps an oversized requested range at the existing 500-line bound and returns useful evidence immediately. If the file continues beyond that range, a footer explicitly identifies requested lines that were not read. If EOF is reached first, the existing EOF marker suffices. Reversed ranges, absent files, out-of-range starts, cancellation, path resolution, binary checks and byte/line output bounds retain their handling. Integer-boundary cases are tested without overflowing the endpoint calculation.

The strict fileops library contract remains unchanged. This modifies only the read_lines tool; it does not raise read_many limits. Valid read_lines requests produce identical output, including their existing EOF metadata. No system instruction, schema, helper inference, cache, provider-specific path or Zen transport change was added. Output omission and unread-range markers are separate: the latter does not claim that every captured line survived display compaction.

## Controlled live runs

`scripts/read-range-cap/main.go` creates isolated fixture files and supplies the actual result of a fixed initial read to the real agent loop. The oversized request is always 21–521. The short fixture ends at line 25 with RetryLimit=6842. In unread-tail, that value occurs at line 521, just beyond the capped range. A valid control reads lines 1–25 of the short fixture. The registry exposes read_lines and saved-output retrieval, with no executable shell or write tools.

| Model / case | Model calls before → after | Tool attempts before → after | Seconds before → after | Input tokens before → after |
| --- | --- | --- | --- | --- |
| Qwen, short file | 2 → 1 | 1 → 0 | 7.021 → 2.142 | 1663 → 741 |
| Muse, short file | 3 → 1 | 3 → 0 | 14.843 → 2.196 | 4546 → 1067 |
| Qwen, unread tail | 2 → 2 | 2 → 2 | 13.304 → 15.500 | 5262 → 8953 |
| Qwen, valid control | 1 → 1 | 0 → 0 | 2.486 → 2.390 | 870 → 870 |
| Muse, unread tail | unavailable → 4 | unavailable → 3 | unavailable → 8.651 | unavailable → 16542 |
| Muse, valid control | not run → 1 | not run → 0 | not run → 3.470 | not run → 1187 |

All ten completed answers were correct. The eleventh attempted case, Muse baseline unread-tail, received HTTP 429 before an answer. The harness then stopped, so its baseline valid control was not run. No retry was issued to evade the provider limit, and neither incomplete control is used for a comparative claim.

The short-file result grows from a 69-byte error to 108 bytes of evidence; both candidate models answer without another tool. Muse baseline also attempted unavailable ctx_execute and an out-of-range read, so its larger difference includes recovery choices beyond the original cap error. Unavailable tools were rejected and did not execute.

The unread-tail control demonstrates a cost: the candidate supplies 7,562 bytes immediately and carries them through another request. Qwen still used two model calls and consumed more input. It correctly read the missing line but also read previously unrequested lines 1–20. Muse also read that prefix and attempted an unavailable shell tool. This change therefore does not promise fewer calls, tokens or lower latency for every oversized read. Valid-control initial results remain identical at 408 bytes.

Models: qwen3.8-27b-uncensored via LM Studio and muse-spark-1.3-contributor-free via Zen, reasoning requested at low. First run pair: Qwen baseline and Muse candidate; second pair: Qwen candidate and Muse baseline. Different providers ran concurrently, with sequential cases per provider. Each model/case has one paired sample where available. The measured duration starts after the fixed initial read; input totals include cached tokens and are not billing estimates. The before build restores only read_lines.go via a Go overlay. These are controlled continuations, not unconstrained project sessions.

## Normal-read overhead and verification

The existing read_lines benchmark measured two 500-iteration samples per variant on Windows / Ryzen 7 5800X3D. Complete 20-line reads measured 0.615/0.582 ms before and 0.580/0.619 ms after. Reading the first 20 of 20,000 lines measured 0.619/0.569 ms before and 0.635/0.576 ms after. Every sample used 181 allocations/op. These are local tool measurements with small timing variation, not an established local speedup.

Tests verify short-file recovery, the 500-line bound, explicit and retrievable omitted lines, normal-result parity, large integer endpoints, errors/cancellation, combined output-size omissions and delivery through native and thin/sentinel agent protocols. The strict library test still rejects oversized ranges.

The first full run exposed an unrelated wall-clock assumption in TestStore_LastForCwd: consecutive inserts may have equal timestamps, while production sorting breaks ties by ID. Its fixture now sets explicit activity timestamps, as the adjacent list test already does. Production session selection was not changed. The final `go test -timeout=90s ./...` and `go vet ./...` passed.

Artifacts under `.tmp/read-range-cap/` retain the failing regression, source snapshots/overlay, all live results (including 429), comparison, benchmark, initial/final full checks, build/smoke and installation verification. The test harness is under `scripts/read-range-cap/`.
