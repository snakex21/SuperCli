# Verified operation receipts and request preparation

## Behavior

The loop keeps a bounded register of successful operations for the current user instruction. Only a registered tool's explicit `ReplaySuccess` callback can make an operation eligible; result text and website content cannot declare completion or permissions. Receipts enter the register after ordinary result verification. A small request-only tail names the latest completed outputs and asks the model to continue only unmet requirements. It does not infer that an entire multi-part request is complete after its first file.

Identical download arguments can now produce a verified inert success instead of a misleading `destination_exists` failure. Before reuse, the downloader checks current destination authorization, native file identity, regular-file status, size and streaming SHA256. It checks identity and the path again after reading. Changed, replaced, unauthorized or cancelled outputs never yield success; a missing file proceeds through ordinary download execution. Reuse makes no HTTP request and never overwrites a file. The first successful save uses the hash already measured during streaming, without reading the body again.

The register is scoped to one Run, capped at 16 entries and 1 MiB, with at most four short receipts in the prompt tail. New instructions, registry changes, unknown mutations and background reports clear it. Same-workspace worker effects invalidate ancestor caches before and after execution, including partial errors and cancellation. This invalidation is separate from successful-repair accounting. Read-result memo and effect replay are mutually exclusive, so cached data cannot replace a current refusal from a replay check.

Search contracts and declared-download results now explain the next operation more directly: search finds missing candidates or external references; fetch reads a known page; download saves a known file. Searches for URL references remain real searches. No website/model special cases, search redirection, guessed URLs or new model round trips were added.

## Request processing and RAM

The preceding live receipt showed only 2–6 ms of CLI context preparation and request encoding per turn. Provider wait/streaming consumed about 92–97% of the turn. The stable native tool-schema prefix and bounded schema caches already worked. Reducing model decisions is therefore more consequential than shaving additional microseconds off tool dispatch.

One measured allocation improvement was worthwhile: the reasoning decoder now recognizes a canonical single-field chat payload and decodes its string directly, avoiding an intermediate JSON map and RawMessage copy. Noncanonical spacing, escaped keys, duplicates, other provider formats and malformed inputs retain the existing parser behavior.

Repeated 150 ms benchmarks on the same 512 KiB native-reasoning history measured:

| Metric | Before | After |
| --- | ---: | ---: |
| Request pipeline | 8.75 ms average | 6.10 ms average |
| Allocated bytes/request | 2.22–2.36 MB | 1.06–1.31 MB |
| Allocations/request | 136–138 | 56–59 |
| Wire bytes | 525,400 | 525,400 |

This is roughly 48% fewer temporary allocation bytes and 30% less time in this synthetic pipeline. It is not a claim about total process RAM, model weights/KV memory, GPU memory or end-to-end generation time. Full history, native continuation state, thinking settings and request bytes remain unchanged.

Benchmark evidence: `.tmp/optimization-oct8-2026/go-oct9-native-replay-baseline-0.log` and `go-oct9-native-replay-after-{0,1,2}.log`.

## Single live Qwen run

Receipt: `.tmp/gif-followup-live/run-3151741449/receipt.json`. Normal GUI session reconstruction, automatic delegation, repository preflight and maximum thinking were retained. All output destinations were isolated under `.tmp`; no media URL or fake tool result was supplied to the model.

| Request | Wall time | Model calls | Tools | Failures | Tools after final save | Model calls after final save |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Initial GIF | 74.669 s | 4 | 5 | 1 | 0 | 1 |
| Another GIF in a new folder | 72.476 s | 6 | 5 | 0 | 0 | 1 |
| Two different GIFs | 58.961 s | 5 | 6 | 0 | 0 | 1 |

All requirements passed: four valid animated GIFs (51, 90, 16 and 39 frames), correct paths, sizes and SHA256 after rereading, and distinct hashes in the final pair. The model performed no tools after the last successful save in any turn. There was no repeated download. One identical search used read-result memo without HTTP; effect replay was unnecessary in this conversation and is verified separately by deterministic filesystem tests.

There are remaining inefficiencies: four searches involving known URLs, plus an unnecessary recall/list-directory attempt in the first turn. The out-of-workspace directory listing was correctly refused and the explicitly authorized download subsequently succeeded. There were no helper/auxiliary model calls, ceiling failures or unfinished final replies.

The run used 15 main calls and 148,388 input tokens versus 18 calls and 169,839 input tokens in the prior conversation. It took 206.50 seconds versus 187.51 seconds. Different selected files and generated reasoning make this an uncontrolled comparison; it demonstrates reliable completion and fewer calls, not a general wall-time speedup. The later cache exclusivity and worker barrier corrections affect combinations/background cases absent from this live conversation and are covered by deterministic regressions.

## Validation and installation

Full suites for agent, tool registry, web tools, LLM, CLI application, TUI and GUI passed, as did vet. Replay tests cover modified content with preserved size/time, native replacement, directory/symlink substitution, revoked exact-file permission, missing output, cancellation before/during hash and while waiting for a mutation lock. Loop tests cover paired protocol results, ordinary verification failures, multiple requested outputs plus inspection, bounded storage, Run reset, denied replay overriding read cache and partially failing worker effects.

Build, installation and help/source hashes are saved under `.tmp/completed-operation-fix`. Both portable executables are replaced with recoverable backups after validation. Checkpoint cleanup preferences and existing application data are not changed in this installation. Running instances require a restart to load the new code.
