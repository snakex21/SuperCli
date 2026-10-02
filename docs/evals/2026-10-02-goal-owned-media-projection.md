# Owned media projection during request preparation

Baseline: b754435 (dev10), Windows amd64, Ryzen 7 5800X3D, Go benchmarks with GOMAXPROCS=2. No model, network, screenshot, application launch, image decoding or configuration change was used in this round.

## Finding and change

Request preparation previously copied the entire projected message slice when it encountered any active or dormant image, then copied those messages again into the independently owned outgoing request. A conversation with two old image references could therefore incur another full-history allocation on every request.

Media projection now updates the already-owned request slice. A shared parts projector still copies image-bearing parts and snapshots active image references. Other parts, including opaque native reasoning, retain their existing identity and bytes. Dormant images keep the same reloadable text markers.

Leading system media is projected before merging system text with the frozen catalog. Any such image disables canonical cached-history pricing, including an active image that the existing text merge omits. For light routes, media-aware window pricing applies only during request assembly. The existing shared estimator, pruning indices, compaction view and raw-history pricing remain unchanged.

There is no new cache or resident history copy. Tool definitions, provider transforms, native reasoning policy and the special Zen path are unchanged.

## Evidence and measurements

A bounded read-only metadata query inspected six completed saved sessions. Four contained no image messages; S04 had 18 messages and four image messages; S14 had 1,859 messages and two image messages. Replay fixtures retain native JSON strings and use the actual history normalization and request preparation code with a small synthetic registry. They do not infer the hidden/compacted state of the original running session. Full canonical coordinator replays are stress cases, not evidence that a live provider received the entire archived transcript.

The matched sequence was: two next-request estimates, tool definition construction, request assembly with pricing, and tool-cost addition. Benchmarks ran three samples per case, 300 ms per sample, with one fixed request timestamp in both overlays. Values below are sample medians.

| Saved replay | Baseline B/op | Candidate B/op | Baseline allocs/op | Candidate allocs/op | Baseline µs/op | Candidate µs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| S04 coordinator | 27,571 | 25,523 | 62 | 61 | 15.85 | 15.61 |
| S12 coordinator, no images | 1,119,107 | 1,119,110 | 56 | 56 | 1,545.92 | 1,569.19 |
| S14 coordinator | 1,719,734 | 1,506,737 | 215 | 214 | 1,417.52 | 1,398.93 |
| S14 with current active image | 1,719,935 | 1,506,935 | 217 | 216 | 1,415.56 | 1,382.01 |
| S14 with old image range hidden | 504,745 | 504,745 | 191 | 191 | 234.62 | 226.44 |
| S14 chat window | 1,513,679 | 1,300,265 | 219 | 210 | 263.49 | 234.54 |

The long image-bearing replay saves approximately 213 KB of allocation per preparation sequence: 12.4% for coordinator and 14.1% for the light chat window. Coordinator CPU differences are small; controls vary by a few percent and do not justify a general speed claim. The chat median improved by about 29 µs. These are allocation-traffic measurements, not application RSS or persistent-memory savings.

All six projected message/tool JSON snapshots were byte-identical, including hashes, prepared token estimates, image markers and native parts. No input-token reduction or extra/fewer model turns occurs. The unchanged outgoing content also means this result does not explain or claim to fix the previously observed LM Studio generation/prefill slowdown.

## Preservation checks

The private overlay compares the old preparation ordering against the candidate directly. Checks cover a dormant image name that crosses the chat-window threshold, active images, hidden ranges, original pruning indices, native parts, cold/warm estimates, append/edit changes and leading multi-system messages.

Durable tests in request_media_projection_test.go preserve:

- Exact outgoing message content against the previous read-only media-first ordering, across thin/full, stable/dynamic, hoist/non-hoist and all four routes.
- Active/dormant leading and trailing images, nil image parts, Unicode names, opaque signed/native payloads and warm/append/edit state.
- The different existing window decisions for raw shared estimator/pruning versus media-projected outgoing requests at the threshold.
- Independent active-image snapshots surviving live-history deactivation; canonical history remains untouched.

The durable synthetic benchmark is reproducible without private session data. No provider call is made by either fixture.

Production validation after the port:

    go test ./internal/agent -count=1
    go vet ./internal/agent

Both passed. The agent suite completed in 16.319 s.

Private reproduction artifacts are under .tmp/goal-runtime-round8-2026-10-02: the baseline/candidate overlays, bounded saved fixtures, redacted metadata and hash/measurement outputs, and portable command scripts. Raw user content and session IDs are excluded from this report and tracked test fixtures. Once production was ported, no further benchmark or model request was run.
