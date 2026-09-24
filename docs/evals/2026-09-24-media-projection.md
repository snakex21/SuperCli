# Copy only projected image parts — 2026-09-24

## Scope

Every provider request passes through mediaProviderView. Previously it copied
the entire message slice and every nonempty Parts slice, even for a conversation
containing only text or native reasoning. This happened before providerMessages
allocated its final request slice.

The projection now returns a read-only view when no image needs transformation.
On the first image it copies the message slice, and copies Parts only for
messages with non-nil image refs. Active image refs still get independent copies
because accepting the request deactivates the live-history originals. Dormant
images still become the same reload handles. The dormant case no longer creates
an escaping image copy that is immediately discarded.

There is no cross-request cache, invalidation state, prompt change, extra model
call, history truncation or new setting. CLI/TUI/GUI and local/cloud models use
the same agent path. The special Zen transport and protocol are untouched.

## Measurements

Windows / Ryzen 7 5800X3D. Synthetic histories; construction excluded from timing.
Initial measurements used five 200 ms samples with the default GOMAXPROCS=16.
A second pair reversed the order (after, then before via a Go source overlay),
using five 250 ms samples and -cpu=1 to reduce scheduler/parallel-GC variation.
Medians of the second pair:

| Case | Before | After | Allocated bytes before → after | Allocations before → after |
|---|---:|---:|---:|---:|
| Media projection, 80 plain-content messages | 1.385 us | 0.187 us | 9,472 → 0 | 1 → 0 |
| Media projection, 80 text-part messages | 3.523 us | 0.251 us | 13,312 → 0 | 81 → 0 |
| Media projection, 80 messages, one image | 3.766 us | 1.539 us | 13,536 → 9,632 | 84 → 4 |
| Media projection, 80 messages, image in every message | 17.069 us | 11.828 us | 28,673 → 24,193 | 241 → 201 |
| Media projection, 1000 plain-content messages | 19.204 us | 2.358 us | 114,688 → 0 | 1 → 0 |
| Media projection, 1000 text-part messages | 56.017 us | 3.407 us | 162,688 → 0 | 1001 → 0 |
| Media projection, 1000 messages, one image | 59.373 us | 21.291 us | 162,919 → 114,853 | 1004 → 4 |
| Media projection, 1000 messages, image in every message | 227.923 us | 172.969 us | 354,707 → 298,703 | 3001 → 2501 |
| Context preparation, 80 text-part messages | 31.638 us | 28.201 us | 53,882 → 40,570 | 102 → 21 |
| Context preparation, 1000 text-part messages | 185.739 us | 125.431 us | 308,488 → 145,791 | 1022 → 21 |

Context preparation includes two request estimates, tool definitions, final
provider message assembly and its token estimate. It excludes pruning, model
inference, network transfer and provider serialization. Each text-part message
in that fixture contains 900 bytes. The image-heavy helper fixtures alternate
active/dormant refs and do not load image pixels.

For the 1000-message preparation fixture, this is about 32% less time and 53%
less allocated memory. Allocation bytes are cumulative per operation, not peak
or resident RAM.

### Mixed controls and limits

The original short-history benchmark uses Content strings and 80 dialogue
messages, so there are fewer Parts allocations to eliminate. At -cpu=1 its
native-tool variant increased from 31.862 to 34.180 us (about 7%), while its thin
variant fell from 23.808 to 22.059 us (about 7%). Allocation bytes decreased in
both cases.

The initial default-CPU pair was noisy: 1000-message preparation fell from
0.713 to 0.324 ms, but the image-in-every-message helper increased from 0.440 to
0.655 ms and the short thin control increased from 24.963 to 40.627 us.
The reversed single-CPU pair improved those cases. All raw results are retained;
there is no claim of a consistent whole-preparation speedup for short histories,
nor a percentage improvement to end-to-end model response time or token cost.

## Validation

- The former projection is retained as a test oracle. Every prefix of a mixed
  history yields the same message values: text, native reasoning, tool calls,
  tool results, multiple active/dormant images, and invalid nil image parts.
- Serializing canonical history before/after projection proves it is unchanged.
- Accepted image snapshots survive live-image deactivation and data clearing.
  Transformed Parts have independent storage; reactivation affects the next
  request without corrupting an earlier one.
- Existing repository-image, worker continuation, reasoning-history and stable
  prefix tests pass.
- go test ./..., go vet ./..., CLI build and GUI build pass.

No live-model comparison was needed: provider-visible message values are
unchanged, and the measured work happens before inference.

## Reproduce

go test ./internal/agent -run ^$ -bench
'Benchmark(MediaProjection|ContextPreparationParts|ContextPreparationWarm)$'
-benchmem -benchtime=250ms -count=5 -cpu=1

TEMP/TMP, build cache and generated evidence are under the application folder.
Before-source overlay, complete samples, medians and validation outputs:
.tmp/media-projection.
