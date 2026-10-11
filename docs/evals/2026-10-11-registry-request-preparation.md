# Reusing immutable validators and requested contract rendering

This follow-up reduces repeated client preparation across providers. It changes
neither model selection nor reasoning effort, compaction instructions, retained
history or tool execution policy. It does not shorten the historical 116-second
inference-runtime pause or demonstrate faster model generation.

## Changes and lifetime

Fresh registries reuse eligible, private `compiledToolSchema` plans by the exact
schema source string. The uncached compiler is unchanged. Registration still
validates each Tool, installs its actual callbacks and maintains local activation,
revision, repair, verification, permissions, workspace and output store. Only
`Register` and `EnsureReadOutput` use the new cache; existing `RegisterFrom`
continues to share immutable plans directly.

The process-local cache holds at most 128 entries and 4 MiB of conservative
residency accounting. This is not an exact Go heap or RSS limit. Source keys are
owned copies, with an 8 KiB source limit, 128 KiB entry accounting limit and
additional graph, depth, retained-text and numeric backing-capacity limits.
Accounting includes a source-scaled reserve for map allocation hints: repeated
types can leave a large backing map despite few unique entries. Regex programs,
invalid/empty schemas and oversized plans use the original compiler without
caching. FIFO eviction clears its key references; cache hits do not renew order.
Compilation happens outside the cache mutex; concurrent publication selects one
immutable plan. No files, credentials or project objects are stored in this cache.

A loop also retains its latest rendered requested-tool contracts. Each lookup
still evaluates current route/dispatcher gates, reads current registry values and
filters current native contracts. Only an exact ordered match of all ToolDef
values reuses the previous text. The original renderer and emitted bytes are
unchanged, including numeric literals and whole-list legacy fallback. The cache
holds at most 32 definitions, 64 KiB of owned source strings and 128 KiB of rendered
text. Larger outputs are emitted in full and are not retained. An atomic presence
flag avoids taking the payload mutex merely to clear an already empty cache;
publication, comparison and payload clearing remain under the mutex.

## Measurement

Windows amd64, Ryzen 7 5800X3D, Go 1.26.2, `GOMAXPROCS=1`, `-p 1`. Compilation
finished before timing. Five paired runs alternate before/after and after/before;
ordinary benchmarks use 200 ms, first-registration controls use one operation in
a fresh process. The baseline uses frozen working-tree sources from before this
follow-up, preserving earlier completed optimizations. New cache files are
removed from the baseline through a Go overlay. Source and binary SHA256 manifests
are retained in the private portable stage.

The registry fixture installs 24 actual application tools without executing them.
The request fixture performs the loop's two threshold estimates, then
`prepareToolRequest` and `prepareProviderMessages(true)`, combining their exact
token estimates as production does. Its late generic workflow contracts are
admitted after the native prefix freezes: 10,683 tail bytes, 17,823 request tokens,
12,100 history tokens. The empty-tail control has 14,586 request tokens. Those
counts match both variants in every final sample. Worker controls use the real
restricted-registry setup; the GUI control builds the complete engine loop over
portable configuration and the genuine tool registry, with no model completion.

Final medians, all five pairs:

| Measured operation | Before | After | Faster pairs | Allocated bytes before → after |
| --- | ---: | ---: | ---: | ---: |
| Repeated fresh registration, 24 real tools | 511.850 µs | 246.581 µs | 5/5 | 343,025 → 145,540 |
| Request preparation with late contracts | 263.085 µs | 22.082 µs | 5/5 | 175,345 → 51,931 |
| Code worker registry setup | 22.962 µs | 6.455 µs | 5/5 | 17,233 → 4,624 |
| General worker registry setup | 22.254 µs | 6.201 µs | 5/5 | 17,072 → 4,464 |
| Full GUI loop construction, no summary call | 6.063 ms | 5.064 ms | 5/5 | 867,676 → 423,164 |
| Request preparation without requested contracts | 6.760 µs | 7.219 µs | 2/5 | 17,128 → 17,128 |
| Continuously changing plugin schemas | 7.205 µs | 8.961 µs | 2/5 | 6,468 → 6,629 |
| First registration in a fresh process | 677.200 µs | 667.800 µs | 2/5 | 345,392 → 358,536 |

The measured GUI construction improves 16.5%, repeated registration 51.8%,
nonempty-tail request preparation 91.6% and worker setup about 72%. These are
operation-specific CPU/allocation gains, not whole-task/model speed claims.
First-registration ranges overlap substantially (615–926 µs before, 633–1,098 µs
after), so there is no demonstrated first-use latency improvement. It allocates
about 13 KiB and 29 allocations more to populate the cache. Empty-tail medians
add 0.459 µs with overlapping ranges and unchanged allocation counts. Changing
schemas add 1.756 µs and one allocation per registration; unique plugin traffic
therefore has a cache-miss cost. These cases are included rather than discarded.

The initial diagnostic series found unnecessary empty-cache locking. After removing
that locking, the complete paired series was rerun. The initial series is retained
separately and is not pooled into the final table. One earlier incomplete benchmark
was rejected because its synthetic plugin tool omitted the required description.

The user's VM work was not interrupted or reconfigured. No real inference,
warm-up, forced eviction, memory pressure or server/cache changes were used.
Passive OS free-physical-memory endpoints for the final measured interval were
25,373,253,632 → 25,346,732,032 bytes. Endpoints cannot attribute memory activity to
the VM or any specific process and do not measure intermediate pressure. Alternated
pairs and explicit controls reduce interference; this is not a clean-machine
benchmark or evidence of faster prompt processing.

## Validation and delivery

Tests passed for tools/core, tools, agent, webgui and app. The initial full test run
caught an incorrect new test fixture: duplicate enum values are rejected by the
existing compiler. It was replaced by accepted repeated type values; core tests
then passed against the unchanged compiler. Following the atomic empty-cache
optimization, agent, webgui and app were rerun and passed. Final `go vet -p 1`
passed for all five packages. Both final native binaries built and passed CLI
version/GUI help checks; Go's flag help is correctly expected on stderr with exit
zero. Formatting and final diff checks are separate from runtime measurements.

New tests compare cached compilation/preparation with the original compiler,
concurrent validation/coercion, FIFO/byte bounds, source ownership, large numeric
backing allocations, callback/repair/permission isolation and independent output
stores. Rendering tests use an independent pre-cache byte oracle, including
Unicode, malformed schemas, duplicate JSON keys and numeric literals. They cover
late registration, changed schema/order, registry replacement, native promotion,
new Run, capability/route/dispatcher gates, reasoning continuation, fresh live
context and exact message-token accounting. These are deterministic contract tests,
not an end-to-end model quality evaluation.

Portable CLI and GUI binaries were replaced atomically with SHA256-verified
backups. Existing running applications were not stopped; restart to load them.
No commit, tag or new release was created for this follow-up.

Private artifacts: `.tmp/efficiency-followup-2026-10-11/` contains frozen sources,
28 candidate source hashes, overlay, four benchmark binaries, 30 final benchmark
logs (80 samples), original diagnostic logs, validation/build records and the
installation receipt. The stage stays beside the application; no application data
was written to profile directories by these changes.
