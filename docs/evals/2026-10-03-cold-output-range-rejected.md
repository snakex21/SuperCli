# Cold retained-output range experiment: rejected

Baseline: 809258e187488e25e5ac4699f3052162359577e7. Windows amd64, Go 1.26.2, CGO disabled, GOMAXPROCS=2. All artifacts and test databases stayed inside the repository.

The retained-output audit suggested reading only the requested byte range from SQLite after a process/session reload. The production cold path restores the whole output and warms its bounded LRU. An ignored Go-overlay prototype added an optional persistence range reader using BLOB substr, kept the public read_output result/footer contract, and did not warm that LRU. Neither the optional interface nor the implementation is shipped.

## Public pipeline measurement

The fixture called the actual SQLite store and public ReadOutputTool, not only a sliced byte buffer. Baseline/candidate/candidate/baseline order, 100 ms per benchmark case, -benchmem, two repetitions per version. Complete paging used consecutive 8 KiB ranges; cold-one used 128 bytes. Each case began with a fresh cold output store except the explicit warm control. Figures below are the two measured ranges, not confidence intervals.

| Output/workload | Baseline | Candidate | Effect |
|---|---:|---:|---|
| 1 MiB, one cold range | 402–405 µs; 1,051,251–1,051,259 B/op | 261 µs; 2,536 B/op | Large one-off allocation reduction |
| 1 MiB, eight concurrent cold reads | 474–490 µs; one whole SQL read | 4.52–6.96 ms; eight range SQL reads | Slower concurrent retrieval |
| 1 MiB, eight repeated reads | 434–441 µs | 1.86–1.91 ms | Lost warmed-cache reuse |
| 1 MiB, complete paging | 0.879–0.945 ms; one SQL read; 2.34 MB/op | 32.1–34.5 ms; 128 SQL reads; 4.89 MB/op | Slower and more cumulative allocation |
| 4 MiB, one cold range | 2.99–3.18 ms; 4.25 MB/op | 2.47–2.57 ms; 51.8 KB/op | Smaller one-off allocation |
| 4 MiB, complete paging | 4.21–4.34 ms; one SQL read; 9.41 MB/op | 1.326–1.358 s; 512 SQL reads; 44.75 MB/op | Severe repeated-read regression |
| Prewarmed cache, eight reads | 14.5–16.3 µs; zero SQL reads | 15.0–16.8 µs; zero SQL reads | Existing fast path retained |

B/op is cumulative Go allocation per operation, not native peak RSS or a claim about the GUI process group. The prototype still asks SQLite to access the large stored value repeatedly; range-shaped results alone do not guarantee bounded database work. It has no performance case for replacing the production cache warm-up.

## Correctness and decision

Both versions passed public text/footer parity, offsets and limits, UTF-8 boundaries, NUL and arbitrary invalid byte sequences, unusual long continuation runs, cancellation and missing-output controls. All four benchmark processes completed successfully. SHA-256 comparisons confirmed the production output-store, persistence, SQLite feature and module files remained unchanged.

Rejected: keep the existing restore-and-cache path. A future design would need to preserve reuse/coalescing while reducing the first allocation, and demonstrate gains on the same complete workloads. This experiment does not demonstrate fewer model turns or faster model prompt processing.

Local evidence: .tmp/goal-cold-output-range-2026-10-03 contains the frozen overlays, exact probe, correctness logs, four benchmark logs, parsed metrics and source manifests. No user session database was modified.
