# Estimate the prepared history once

## Evidence and change

The coordinator estimator prepared the provider history, then always called EstimateVisibleTokens before deciding whether to discard that result and price the projected history. Hidden transcripts also constructed a second identical visible slice. With old native reasoning lacking a token count, this could deserialize reasoning that the request would omit. This is a code/fixture finding, not a measured cause of a saved session slowdown.

The estimator now uses the existing incremental cache only for an unchanged, unhidden transcript. Otherwise it prices the already-prepared history directly. There is no new cache, invalidation policy, instruction, model call or provider-specific path. The special OpenCode Zen behavior is unchanged.

## Equivalence

The regression states the expected history explicitly across hidden/unhidden and retained/discarded reasoning. Repeated estimates match that history; active tool reasoning remains present, the original transcript stays intact, appended messages are counted, and restoring the reasoning preference restores its original cost. The provider receives the same history. Existing context, projection, calibration and reasoning tests also pass.

## Local benchmark

Go 1.26.2, Windows, Ryzen 7 5800X3D; three 300 ms samples per case, warm cache. A sample performs two budget estimates plus request construction and estimation. The synthetic transcript contains 40 previous replies with approximately 30 KB of native reasoning (unknown token count) and 8.7 KB of visible text each; hidden cases add 2000 archived messages. This intentionally exercises a large history, not a typical greeting. Native and thin refer to tool transport.

| History / transport | Before, ms | After, ms | Allocation bytes before -> after | Allocations before -> after |
|---|---:|---:|---:|---:|
| Plain / native | 0.255–0.328 | 0.244–0.268 | ~1,347,250 -> ~1,347,270 | 61 -> 61 |
| Plain / thin | 0.259–0.292 | 0.245–0.267 | ~1,353,800 -> ~1,353,810 | 63 -> 63 |
| Hidden / native | 0.731–0.816 | 0.674–0.699 | ~4,018,400 -> ~3,999,370 | 159–160 -> 153–154 |
| Hidden / thin | 0.756–0.793 | 0.690–0.809 | ~4,025,000 -> ~4,005,900 | 162 -> 155–156 |
| Projected / native | 0.851–0.897 | 0.785–0.791 | ~75,570 -> ~75,570 | 142 -> 142 |
| Projected / thin | 0.797–1.090 | 0.774–0.779 | ~82,120 -> ~82,120 | 144 -> 144 |
| Hidden + projected / native | 1.403–1.583 | 0.796–0.813 | ~2,746,900 -> ~104,470 | 241 -> 151 |
| Hidden + projected / thin | 1.439–1.892 | 0.789–0.792 | ~2,753,430 -> ~111,030 | 243 -> 153 |

The robust gain is removing about 2.64 MB of transient allocations and roughly halving the local sample cost for hidden history with old reasoning omitted. The other timing differences are small/noisy; unchanged allocation counts on plain/projected warm history do not establish a meaningful gain there. These are local preparation measurements, not model latency, billing-token savings or a whole-session speedup.

## Validation

Focused tests, go test -timeout=90s ./..., go vet ./..., CLI/GUI builds and CLI --help pass. Local before/after source, benchmark outputs, checks and installation records are in .tmp/context-estimate-projection-2026-09-27/.
