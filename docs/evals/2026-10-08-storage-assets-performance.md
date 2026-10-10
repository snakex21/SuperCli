# SuperCli optimization pass — 2026-10-08

This pass follows a read-only audit of the recent Gemini game-assets session and portable application storage. It changes no provider authentication or OpenCode Zen transport route.

## Evidence

The game session contained 1,703 messages, 845 tool calls and 8 completed turns totaling approximately 226 minutes. Recorded main model usage totaled 70,750,393 input and 1,640,992 output tokens. Exact repeated-argument groups were sparse (24); 326 of 655 command calls involved ZIP processing. This does not justify treating every repeated command as unnecessary or promising a fixed reduction in model turns.

Memory databases occupied 3,559,424 bytes across 26 stores; checkpoint directories occupied 67,776,852,415 bytes. Three unfinished checkpoint staging repositories had no turn records and no Git refs. Those exact directories were removed after retaining their index/config metadata and checking their workspace identities. Gross file bytes removed: 23,801,137,179 (22.17 GiB); about 71 MB of diagnostic metadata remains in the ignored local audit folder. Original workspaces and all recorded undo histories were retained.

## Changes

- Explicit file edits capture only affected paths; arbitrary command captures have bounded preflight. Common direct Git inspection bypasses checkpoint capture. Raw snapshots avoid clean filters, line-ending conversion and duplicate compression. Retained turns pin both snapshots. See [checkpoint evaluation](../../internal/checkpoint/EVALUATION.md).
- `web_download` streams a public direct file URL into a new project file and returns path, size, MIME and SHA256, rather than binary/base64 content. It refuses existing targets before HTTP, checks redirects/size and supports cancellation. GUI, TUI and batch share the tool; dependent reads wait for the download. Intent discovery selects the download tool instead of a webpage reader that merely mentions downloading.
- Memory pattern filtering, sorting and limiting happen in SQLite before decoding unrelated entries. Pattern/briefing wrappers, labels and UTF-8 boundaries obey their rendered context budget. See [memory evaluation](2026-10-08-memory-context-budgets.md).
- Goal verification reports both missing arguments together, removing one avoidable repair round in the recorded three-call failure sequence.
- Native Anthropic conversation requests use prompt cache metadata; one-shot auxiliary calls and gateways including Zen/AnyRouter are unchanged. Signed reasoning remains opaque. Fresh transient reminders stay outside the cache breakpoint. This is an offline wire/compatibility validation, not a live provider speed measurement.
- GUI diff statistics scan line starts without allocating an array of every line. See [GUI evaluation](2026-10-08-tool-change-stats.md).

## Measurements and validation

The 4.6 MB / 240,000-line Node fixture produced identical diff statistics. On Node v24.19.0 its median helper time changed from 14.4606 ms to 2.8603 ms. Temporary heap remaining before the next GC changed from 42,565,784 bytes to 584 bytes. These are helper measurements, not overall WebView2 RAM or frame rate. All 205 Node UI tests passed.

A 330 KB synthetic Anthropic encoding benchmark measured 6.45 ms without cache metadata and 6.37 ms with metadata; neither adds a request or a model turn. Live cache hit rates and TTFT were not measured. The LM Studio endpoint was unavailable during this pass, so no live Qwen performance improvement is claimed.

`go test ./...` and `go vet ./...` passed with portable test/cache directories.
The full checkpoint suite includes exact binary/CRLF restore, conflicts,
legacy conversion diagnostics, symlink refusal and rollback on file/metadata
write failures.

The synthetic checkpoint undo/redo benchmark allocated 768,544 B for 1 MiB
and 766,376 B for 64 MiB. Both use a fixed streaming buffer. These figures
measure Go allocations for the complete roundtrip, excluding fixture setup and
child Git process memory; they do not measure total process RSS. A separate
32 MiB binary regression verifies its SHA256 roundtrip and a Go heap delta
below 8 MiB.

The paired memory query benchmark returned the same three IDs in both
variants: 11,576,035 ns / 9,527,930 B for the legacy scan versus 158,769 ns /
3,640 B for SQL-filtered retrieval. See the memory evaluation for methodology.

TUI search now caches the current filter's immutable results and bounded
previews. The first cache-only implementation cost about 14 ms on a cold query.
The subsequent streaming cold matcher reduced the representative ASCII late
match from 16.579 to 12.270 ms and allocations from 15,326,144 to 67,312 bytes
in the production benchmark. Warm lookup still allocates zero bytes; its roughly
19 ns timing measures lookup rather than full-text scan.
Unicode/end-of-message matching, history mutation/load and copied-model reuse
regressions passed. See [TUI evaluation](2026-10-08-tui-transcript-search.md).

Both TUI and GUI builds passed for windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64. Windows EXEs were installed after source-hash verification; previous binaries are retained in the ignored local audit folder. This is a development update, not a published release.

## Follow-up integration and final validation

- ZIP text reads now accept explicit source parts and a required entry glob,
  returning bounded complete UTF-8 contents without extraction or shell commands.
  Extraction is classified as a workspace mutation and checkpointed in GUI/TUI;
  read/list remain discovery-only. Worker invalidation, failed-command recovery,
  source-write scheduling and undo/redo regressions passed. See
  [ZIP evaluation](2026-10-08-zip-evidence-mutation.md).
- The production session FTS migration removes duplicate source text while
  preserving tokenizer, search semantics and all history. On a consistent real
  database copy, 201 sessions and 18,931 messages retained an identical full-row
  digest. Migration took 1.516 seconds once; copy-only VACUUM reclaimed
  27,516,928 bytes. The original user database was not migrated or compacted by
  the test. Ordinary startup does not VACUUM; reusable pages do not immediately
  shrink its file. See [FTS evaluation](2026-10-08-session-fts.md).
- Production TUI differential tests and the bounded 15-second fuzz run passed
  (9,993 executions). Cold scanning no longer allocates normalized copies of
  full messages for typical short queries. Long/repetitive queries retain the
  previous matching algorithm. See [TUI evaluation](2026-10-08-tui-transcript-search.md).
- The compaction-cache audit remains read-only: available historical metadata
  does not support an exact safe warm-prefix A/B. No extra prompt, history copy
  or model request was added from that audit. See
  [compaction audit](2026-10-08-compaction-cache-audit.md).

The final integrated `go test ./...` passed 71 packages, `go vet ./...`
produced no diagnostics, and all 205 Node UI tests passed.
Both binaries compiled for all five existing targets: Windows amd64, Linux
amd64/arm64 and macOS amd64/arm64. The final operation took 344.605 seconds;
the tool response timed out at 300 seconds, but the completed driver record
confirmed all steps and both installations. Subsequent binary hashes, TUI
`--version` and source digest checks passed. Source digest:
`7490dc9ce400261e032fcf0c5416cb5b0ff55e99b84fdd1f76bf8a92e7962384`.
Installed development version remains 1.0.4. Previous EXEs are preserved.
No Git commit, push or release was made in this optimization pass.

The public Zen probe returned HTTP 429 in 0.928 seconds before any model text
or tool use, with the existing Zen gate enabled. LM Studio refused the local
connection. Thus this pass has measured local fixture/integration improvements,
not a verified reduction in live Qwen/Gemini turns or overall model completion
time. Aggregate completion records are in the ignored portable evaluation
folder; the temporary private-history test copy was removed after validation.

## Subsequent measured improvements

- A folded GUI file-change summary no longer constructs every hidden path node.
  Up to eight paths remain immediately expanded; larger groups construct paths
  only on their first opening, then reuse them. All paths and original ordering
  are preserved. Real archived turns included a maximum of 36,714 changes.
  The matching synthetic fixture reduced initial element count from 36,737 to
  23, construction median from 212 to 72 ms, and retained JavaScript heap from
  about 41.5 to 1.6 MB. Exact expanded HTML and 10,000 common-root cases matched
  the baseline. These are Node/LinkeDOM measurements, not WebView2 process RAM.
  See [file-change evaluation](2026-10-08-gui-file-change-lists.md).
- OpenAI-compatible request assembly reserves the known slice capacities after
  existing history repair. It changes no wire bytes, caller data or routing.
  The 1,703-message synthetic encoding case reduced allocated bytes from 6.38
  to 4.71 MB; the shorter parallel-tool case used 633.5 KB rather than 778.2 KB.
  Whole-request parity includes native parts, tool repair, images, Zen and
  AnyRouter fixtures. This is local allocation work, not fewer input tokens or
  faster model inference. See [request evaluation](2026-10-08-request-preallocation.md).
- Explicit goal task titles can be added atomically in one tool call. Three
  registry/service additions measured 2.218 ms / 33,646 bytes across three calls;
  one batch measured 0.739 ms / 12,723 bytes. Validation and SQL failure tests
  prevent partial writes. The discovered tool schema grows by 268 minified JSON
  bytes; there is no new persistent prompt instruction. The feature enables
  fewer model calls when a model chooses a batch. See
  [goal evaluation](2026-10-08-goal-explicit-task-batch.md).
- TUI tool-result handling no longer formats a marker into an ignored helper
  argument before formatting the visible marker. Baseline/candidate snapshots
  matched exactly over 192 states. The complete folded 1 MiB JSON handler
  median changed from 16.183 to 10.047 ms and from 13.75 to 9.51 MB allocated.
  Canonical messages, rendering, workers and scroll behavior are retained. See
  [TUI handler evaluation](2026-10-08-tui-transcript-presence.md).
- Per-message transcript JSON streaming was measured and rejected for this
  batch. It reduced cold scratch-buffer allocations but made ordinary pages
  roughly 16% slower, introduced many output writes and did not release buffers
  donated by other users of the shared JSON pool. See
  [negative experiment](2026-10-08-transcript-json-experiment.md).

The subsequent integrated validation passed all 71 Go packages, `go vet ./...`
and 208 Node UI tests. The first combined observer operation exceeded its
240-second tool limit; no success was inferred from it. After a single diagnostic
check more than five minutes after launch established that its process tree was
gone, validation was split into independently awaited stages and completed.
No progress polling or duplicate running test suite was used.

Both applications were then compiled for all five targets and the Windows EXEs
installed with checksum verification. The final source digest is
`8dfa9fb12f6197c8bd914bcbc25b094614e01f704c6a36f12ec2b02e6627e41f`.
Installed TUI: 26,557,952 bytes, SHA256
`8a20680b71b97340ebe51172c952d5d6f1c137784b22df06e0a9eb2376835463`.
Installed GUI: 23,447,040 bytes, SHA256
`c78f6cf0cc58b29b694d48da786a91eca4ca9dcaf8c92321cbdf598ce3b33270`.
Both retain development version 1.0.4; old binaries are preserved. `git diff
--check` and the installed TUI version check passed. No commit, push or release
was performed.

A final read-only audit inspected metadata from six recent sessions (2,404
messages and 1,236 tool calls) selected from the last 35 sessions. It found no
additional implementation justified by the observed repeats: ZIP/script repairs
are covered by current bounded readers, downloads by the new download tool, and
the one duplicate read without an intervening user or potential mutation did
not establish a missing tool contract. A neighboring Pi search comparison also
found that existing SuperCli search already returns collected hit lines and
bounded same-response context. No speculative larger context or always-on
prompt instruction was added from either audit.

## Follow-up: GUI diagnostic retention

The final retention audit found one concrete remaining owner of a finished run:
`Engine.diagnosticRegistry` retained the executable base registry, whose
`task.Fn` method retained `AgentTool.ParentLoop` and its model history. Loading
another conversation did not replace this diagnostic reference. This established
retention of the previous loop, not accumulation of every historical session.

The Engine now retains only an on-demand 48-byte diagnostic handle containing
counts, synchronization and revision. It has no reverse reference to Registry,
callbacks, validators, output cache or history. Counts follow all registration
and visibility mutations, including unknown always-on names and discovery;
doctor still reports the complete base registry in orchestrator mode. Active
loops/workers retain their executable tools normally. Existing fixture tests
now use a private builder's real base registry rather than a diagnostic pointer.

Central core/doctor/webgui diagnostic regressions passed, including concurrent
count publication, real delegation and a weak-pointer positive legacy control.
The matched heap experiment seeded a real backend loop with 18 MiB synthetic
history and 2 MiB synthetic inline image data. After explicit GCs, the legacy
median extra retained Go heap was 20,971,968 bytes with the loop still alive;
the candidate median was 16 bytes with the loop collected. This is controlled
retained heap, not WebView2/process RSS or a promised per-session saving.

The 120-tool visibility-pair benchmark measured legacy nil-handle 72.63 ns,
candidate nil-handle 77.96 ns and candidate live-handle 1.912 us, all with zero
allocations. Live counts rescan only after a real registry revision change;
unobserved registries return before scanning. No provider request, tool schema,
model instruction or token was added. See
[retention evaluation](2026-10-08-gui-diagnostic-retention.md).

The final integrated pass including diagnostic retention again passed all
71 Go packages, static analysis and 208 UI tests. All ten application/target
builds completed; the final Windows binaries were installed and their hashes
verified. Final source digest:
`4af9f20b78079d019784f9972e1a6a69d3d9ea6ffd4f6157d188f2b66c88068b`.
TUI: 26,563,584 bytes, SHA256
`195f4b1980045f73479f6beb91fae9ed3809cb17f3fe0a8059587209e073b725`.
GUI: 23,452,672 bytes, SHA256
`ea2589ab3e0ac9e3c198119bbb89a47de6a3bb24ebda7f7d165ac23be210720f`.
Installed development version remains 1.0.4 and old EXEs are preserved.

The goal batch also passed a live Kilo contract smoke test with the complete
production tool description/schema: the free `inclusionai/ling-3.1-flash` model
chose one correct three-title tool call. This adds a live compatibility result;
it does not establish an overall task-speed or Qwen improvement. LM Studio was
unavailable and the Zen probe returned 429, so no additional claim is made for
those providers. No persistent application prompt instruction was added by the
test. See [goal evaluation](2026-10-08-goal-explicit-task-batch.md).

## Follow-up: requested downloads, live thinking and shared memory identity

The next integrated pass exposes the exact registered download contract only for
an explicit direct-URL download request, through existing request-only tool
context. Core tool schemas/cacheable prefix and subsequent ordinary turns stay
unchanged. Offline workflow and target validation tests passed. A live free Kilo
Nemotron run selected the correct download on its first response and saved the
exact public PNG, but did not finish within the isolated four-step safety limit.
Ling attempts were rate-limited; no live speed/turn-reduction claim is made. See
[download evaluation](2026-10-08-download-request-contract.md).

Folded live GUI reasoning defers hidden DOM/markdown work until disclosure. A
matched synthetic stream retains the canonical text and identical first-reveal
HTML, while reducing hidden markdown updates from 188 to zero and hidden DOM
elements from 129 to zero. Open reasoning and final/tool/EOF/history paths have
regression coverage. The measured hidden-update median was 35.34→1.85 ms over the
fixture; this is Node/LinkeDOM work, not WebView2 RSS or provider inference speed.
See [live thinking evaluation](2026-10-08-gui-lazy-live-thinking.md).

GUI memory Stores now share canonical backing DB identity after project
relocation, preserving already observed worker/home bindings. Nine aliases
retain one Store/worker/pool rather than nine. Native SQLite pager-cache median
after a synthetic connection burst changed from 9,252,044 to 2,539,116 bytes;
ordinary cache hits still allocate zero bytes. Eight-alias first-open median
changed from 155.227 to 19.117 ms. These are controlled alias/resource counts,
not general startup/RSS measurements. See
[memory identity evaluation](2026-10-08-engine-memory-store-identity.md).

The small false-truncation-field projection experiment was rejected after
matched tests/benchmarks; production output remains explicit. See
[negative command projection evaluation](2026-10-08-ctx-truncation-rejected.md).

Central verification passed 71 Go packages, vet and 215 UI tests. All ten
application/target builds passed. Windows TUI and GUI were installed, checksums
verified and TUI --version/diff whitespace checks passed. Source digest:
`01c7a33b58de551a544816373aed25f4a91ec57b5953b5d623eec6a79df8d2dc`.
TUI SHA256: `61cca60d2cefc92037ac594a44f61cccd9616abc0ddf0ef9c726e31264f4cf54`.
GUI SHA256: `8e241681f35d708c2d3c4d779ff1429174d4250638c576c02caf9a3a18091fdf`.
The first test observer exceeded its limit without a completed log. After a
completion-notification wait and a justified one-time process check over five
minutes after launch confirmed no Go process, the bounded directly logged test
driver completed. No overlapping test suite or success inferred from timeout.
Development version remains 1.0.4; no commit, push or release.

The user then reported a 103 GB application folder. A read-only audit measured
111,594,039,937 file bytes (103.93 GiB), of which developer .tmp held 65.94 GB
and portable data 44.55 GB. Exact old build/cache cleanup and protected-root
validation recovered approximately 51.2 GiB net at the subsequent measurement.
All 50 undo records remain protected. See
[disk evaluation](2026-10-08-development-storage-cleanup.md).

The Oct9 storage batch completed a guarded offline narrowing/reclaim pilot for
one inactive archive: 3,217 loose objects / 15,968,986,373 bytes removed, all 50
persisted records retained and required lookup parity verified. Latest app-only
size is 38.122 GiB (65.808 GiB net reduction from the original audit). Remaining
checkpoint/archive size is 24.850 GiB. Automatic ~1 GiB quota/expiry is still
pending active-root/worker/cross-process integration; no claim of a lifetime
limit is made. Open now rejects corrupt/unreadable checkpoint metadata. Full
71-package tests, vet, 215 UI checks and ten platform builds passed; Windows
GUI/TUI were installed and verified. Source digest:
`b9d5a6ff08f98c04bcd0942b649e306834a9ba63b3b712064e485e5af2bc6c67`.
TUI SHA256: `a22b129104400c5375b46b9d062bdf5f853da5942eb28a5e02ee1ebd5b6e1f12`.
GUI SHA256: `5b9288e818c53dfd8f8b04ef9903cfd2dede6cfb0cbb6ddb098b65e0e20bfbbe`.
Work is paused for today at the user request.
