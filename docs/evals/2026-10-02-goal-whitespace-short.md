# Short non-whitespace byte counting

The token estimator repeatedly prices short strings such as message fragments and tool names. Its <64-byte scalar switch now uses a private 256-byte classifier instead. The >=64-byte branch still uses the same four standard-library Count scans. This change preserves the exact counted bytes, token estimates, rounding, native reasoning token handling and provider payloads; it adds no instructions, model calls, history cache, dependencies or unsafe code.

Only ASCII space, tab, LF and CR are excluded. All other byte values remain counted, including Unicode whitespace, non-ASCII UTF-8 bytes, controls and invalid UTF-8. No Unicode validity scan or rune conversion is needed. The classifier is read-only in use and has no pointers. Its cost is 256 bytes of static process data and 0 per-call allocations; it does not retain history or increase worker residency with cached text.

## Measurements

Windows/amd64, Go 1.26.2, Ryzen 7 5800X3D, GOMAXPROCS=2, CGO_ENABLED=0. No model/provider/GPU requests. All measurement files and caches were under the portable repository .tmp directory. The post-port comparison below uses the durable benchmark sources with only the counter implementation changed by a baseline overlay: no experimental function variants or runtime selector. Medians of 3 repetitions at 100 ms.

|English input|Baseline string|New string|Baseline raw bytes|New raw bytes|
|---|---:|---:|---:|---:|
|15 bytes|9.735 ns|9.248 ns|9.660 ns|9.032 ns|
|31 bytes|17.590 ns|17.590 ns|17.620 ns|17.480 ns|
|63 bytes|37.310 ns|33.340 ns|37.580 ns|33.650 ns|
|8192 bytes|453.200 ns|471.300 ns|464.200 ns|473.100 ns|

Across English, Polish, multilingual and arbitrary-byte durable fixtures, median short-input reductions over both input types were 9.7%, 4% and 9% at 15, 31 and 63 bytes. Individual short cases ranged from no improvement to about 35% faster. All direct cases used 0 B/op and 0 allocs/op. This is a small CPU optimization, not a stable universal 40–50% win.

Earlier exploration with an expanded benchmark binary showed larger short-loop gains (roughly 39–50% median), including code and whitespace corpora. The durable benchmark did not reproduce that magnitude, so the smaller post-port measurements above are the conservative reference. The raw exploratory and post-port logs are both retained; no unverified explanation for the difference is claimed.

The unchanged long branch varies with measurement conditions: durable 8192-byte cases ranged from 1.5% faster to 7.5% slower. No long-input speedup is claimed. Exploratory pure scalar/table, variable-shift bitmap, ASCII-branch and safe SWAR replacements were about 4–10x slower on long data. Go's stdlib Count already uses vectorized byte scans on this CPU, so replacing its four passes with one portable scalar pass was rejected. Lowering the SIMD threshold to 32 had mixed results and was also not selected.

## Complete request preparation

The benchmark runs the actual estimator twice for prune/compact checks, builds real tool definitions, assembles provider messages and prices the exact request. It uses 40 conversation turns with distinct indexed body text, 63 registered tool definitions, native reasoning Parts, the existing append-only estimator and the tool-definition snapshot. Warm mode retains that existing estimate, cold invalidates it before each sequence, and projected mode removes prior native reasoning while traversing the current message shapes. It excludes serialization, transport, inference and generation.

Assistant texts were 48 or 8192 bytes, native payloads 4 times that length. The arbitrary-byte corpus deliberately exercises invalid UTF-8/native fallback byte counting; it is a synthetic estimator fixture, not evidence that invalid native payloads would be accepted by an inference API.

Both comparison orders are retained. The first set ran baseline then candidate (3 × 200 ms per case); the second candidate then baseline (3 × 100 ms) to check CPU drift.

|48-byte visible text|First baseline -> candidate|Reverse baseline -> candidate|
|---|---:|---:|
|English warm|8.965 ->8.288 us|8.667 ->8.530 us|
|English cold|12.984 ->11.769 us|13.096 ->12.807 us|
|English projected|30.127 ->26.569 us|29.566 ->27.585 us|
|Polish cold|13.397 ->11.763 us|13.155 ->12.396 us|
|Polish projected|30.981 ->26.568 us|29.977 ->29.348 us|
|Multilingual cold|13.436 ->11.601 us|13.008 ->11.969 us|
|Multilingual projected|30.662 ->26.453 us|29.883 ->26.889 us|
|Binary cold|14.118 ->11.463 us|14.282 ->11.620 us|
|Binary projected|33.755 ->26.629 us|32.949 ->27.690 us|

Cold/projected short-text preparation improved in both orders; savings are small in absolute terms, roughly 0.3–7 microseconds. Warm gains are smaller and noisy: reversed binary warm was 8.570 ->8.762 us (2.2% slower). With 8192-byte bodies, most complete-sequence changes were within 0–5%, so no universal end-to-end latency improvement is claimed.

All request-sequence allocation counts stayed the same: canonical warm/cold 13,289 B/14 allocs, projected 53,230–53,231 B/137 allocs (rounding variation). The benefit is less CPU work for small byte strings, not fewer provider tokens, fewer agent turns, faster GPU prefill or a measurable large RAM reduction.

The post-port comparison was repeated with the durable sequence benchmark and a baseline overlay containing only the old meter_estimate.go. Candidate then baseline, 3 × 100 ms per case, with no experimental sources. English 48-byte warm/cold/projected medians were 8.644 ->8.164 us, 13.230 ->11.719 us and 29.490 ->25.999 us. Polish cold/projected were 13.622 ->11.713 us and 35.361 ->25.993 us; multilingual were 13.052 ->11.665 us and 30.076 ->27.555 us. Allocation counts remained unchanged. Long-body cold multilingual/binary were about 1% slower in this final set, reinforcing that no universal large-history gain is promised. These are CLI-only preparation savings, still microseconds rather than provider latency.

## Validation and reproduction

The durable tests check every byte value at 19 boundary lengths through 8192, 3000 seeded random byte slices, six text/binary corpora, deliberately truncated/invalid UTF-8, unchanged input bytes and 0 allocations. They use the existing independent scalar reference rather than the classifier to compute expected values. Existing native-reasoning payload/token tests and agent exact-wire/projected-history estimator tests are included in scoped validation. Experimental alternate algorithms remain only in .tmp.

The durable benchmarks are:

    go test ./internal/llm -run '^$' -bench '^BenchmarkNonWhitespaceCount$' -benchtime=100ms -count=3 -benchmem
    go test ./internal/agent -run '^$' -bench '^BenchmarkWhitespaceContextPreparation$' -benchtime=100ms -count=3 -benchmem

Use repository-local GOCACHE/GOTMPDIR and the existing offline module cache. The measured pre-change counter is preserved as .tmp/runtime-whitespace-2026-10-02-round5/baseline_meter_estimate.go and referenced by durable-baseline-overlay.json, so a local baseline comparison uses the same durable benchmark sources with that overlay. Raw outputs and forward/reverse median matrices are in the same directory. The measured production candidate changes only meter_estimate.go; no Loop/provider/child ownership changes are required.

Scoped integration: both packages passed the counter/native-reasoning/exact-wire/projected-history tests, and go vet passed for internal/llm and internal/agent. No full build, full-suite or model-performance claim is made in this scoped change.
