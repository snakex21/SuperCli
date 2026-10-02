# Goal round: prepared image sampling weights (2026-10-02)

This change prepares horizontal/vertical boundary overlap weights once per image axis. Interior source pixels have weight one. The sampler therefore avoids repeated min/max calculations for millions of pixels while preserving its accumulation order and area normalization. It adds no tool, prompt instruction, provider call, background work or permanent cache. OpenCode Zen transport is unchanged.

## Measurements

The existing BenchmarkPrepareAnalysisImage4K includes image decoding, area scaling and PNG/JPEG encoding on a synthetic 3840x2160 pattern. Four isolated runs used baseline, candidate, candidate, baseline order; each run measured three operations per format, GOMAXPROCS=2 on Ryzen 5800X3D. Approximate means of the two run results:

| Input | Baseline | Candidate | Change |
| --- | ---: | ---: | ---: |
| PNG | 238.07 ms | 178.10 ms | about 25% less CPU elapsed time |
| JPEG | 306.79 ms | 243.49 ms | about 21% less CPU elapsed time |

Temporary allocation increases by 24,576 bytes for the two prepared axis tables in this 4K fixture; allocation counts stay 64 (PNG) and 36 (JPEG). Source decoding and the bounded destination still dominate: about 38.3 MB and 17.3 MB respectively. This trade removes repeated arithmetic without adding idle residency. These numbers are CPU preparation measurements, not provider inference, screenshot capture time, system-wide RSS or universal end-to-end latency.

## Correctness and boundaries

Twelve complete encoded-output SHA-256 comparisons across three odd/integer dimension pairs, PNG/JPEG inputs with alpha patterns, and full-image/cropped inputs were byte-identical to the previous implementation. Prepared overlap coefficients and spans are tested against independent geometric overlap across source/target ratios, including enlargement. Existing thin-stroke, alpha-edge, 16-bit original crop, dimensions, region, byte/pixel budget and cancellation tests pass. Original images, model image limits and preview pixels remain unchanged. Independent review also identified a pre-existing cancellation edge: cancellation during the last sampled pixel could return a completed target from the internal scaler. A final context check and targeted regression test now reject that result; the higher-level encoder already guarded cancellation.

Local artifacts: .tmp/goal-area-weights-2026-10-02/interleaved-bench.json and byte-comparison.json. No GPU, desktop capture, VM, external model or network request was used.

## User feedback

The user relayed that the Linux development package 1.0.4-dev.2 works for the person who reported first-run provider selection problems. This is external user confirmation in that person's environment, not a native Linux test performed here.

## Verification

Scoped media tests, full Go tests, go vet and all 133 GUI tests passed. All ten platform builds passed and Windows TUI/GUI were installed as 1.0.4-dev.3 with matching SHA-256 hashes. See efficiency-goal-round-one.md for combined verification and measurement boundaries.
