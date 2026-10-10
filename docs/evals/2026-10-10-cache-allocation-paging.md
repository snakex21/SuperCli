# Cache allocation and paging follow-up — 2026-10-10

The historical 116.84-second pause really entered cache allocation. Two archived warnings announce eviction of entries totaling **1587.048 MiB**, and **116.793158 seconds remain after the second announcement**. The archive still has no separate end markers for eviction, allocation, checkpoint copying, state save/load or sampler setup. It cannot retrospectively supply exact save-versus-restore durations.

A new **single identical-body request** with event-driven Windows tracing did not reproduce that pause. Selection-to-launch takes **1477.379 ms**. In that interval the native process incurs **7847 hard faults** and reads **474,574,848 bytes (452.59 MiB) from the pagefile**. Independently verified stacks identify deep copying of the cache checkpoint list and its three vector payloads, before the state-save wrapper/direct tail target. One thread has **1001 ms of observed off-CPU intervals starting with the checkpoint-copy constructor on its stack**. This demonstrates checkpoint copying alongside paging on the current preparation path; it does not establish that the old 116 seconds had the same split.

The user reports concurrently installing operating systems and suggests temporary RAM pressure. This is a relevant workload confound consistent with paging, not independently measured attribution to a particular installer or virtual machine. Neither the current observation nor an earlier free-memory snapshot establishes the RAM pressure during the historical pause. No further inference or artificial memory-pressure reproduction is performed.

No production code, model/server setting, thinking level, summary instruction or durable conversation is changed in this follow-up. No speculative cache disabling, context reduction, slot pinning, extra model instance or warm-up is performed. No universal client change proven to remove this backend interval is shipped.

## Historical boundaries

| Boundary | Offset from idle-slot LRU selection |
| --- | ---: |
| First eviction announcement, 779.883 MiB | 11.187 ms |
| Second eviction announcement, 807.165 MiB | 46.627 ms |
| Task 45037 launch marker | 116839.785 ms |

Warnings precede `pop_front()`, so they are not completed-removal timers. The first removal and intervening work fit into 35.440 ms; the second has no observed end. The nearest preceding task on the slot ends **74.76 minutes earlier**, with 22,517 tokens. There is no observed intervening task/defer record in the known grammars. The slot is already idle at selection, which rules out waiting for that selected occupied slot as the entire explanation. It does not prove that every backend queue or logging wait is zero.

The two entry sizes include stored checkpoint payloads. They are not measured transfer volume, new-state size or a measured RAM footprint. Historical prompt evaluation remains **939.07 ms / 4 tokens**, followed by **39271.95 ms / 1367 generated tokens**.

## Runtime source and binary anchors

The active runtime package is ROCm **2.55.0**, declaring llama.cpp **b11511 / fc9ce6b9d52a8504edcb262abc92737c2289f96c**. The archived slow process has no supported build banner, so current package metadata cannot prove historical binary identity. Downloaded source files are checked against official Git blob hashes. The installed DLL exports, direct control flow and x64 unwind ranges provide separate binary evidence for the live trace.

The relevant source path can evict old buffers, zero-initialize new buffers, clone token metadata, deep-copy checkpoint payloads, synchronize GPU work, export the current state, restore a cached state and then initialize sampling. Checkpoint payloads count toward the logical cache budget; original checkpoint copies still owned by the slot, excess vector capacity and transient allocation peaks do not. State serialization uses selected live sequence cells, rather than blindly copying unused full context capacity. [Cache allocation and restoration](https://github.com/ggml-org/llama.cpp/blob/fc9ce6b9d52a8504edcb262abc92737c2289f96c/tools/server/server-task.cpp#L1722-L1878), [checkpoint storage](https://github.com/ggml-org/llama.cpp/blob/fc9ce6b9d52a8504edcb262abc92737c2289f96c/common/common.h#L1263-L1279), [state synchronization](https://github.com/ggml-org/llama.cpp/blob/fc9ce6b9d52a8504edcb262abc92737c2289f96c/src/llama-context.cpp#L4454-L4462).

The API does not expose exact timers for implicit save/load/allocation. Its TRACE cache-update timer covers the combined block. The ordinary INFO task marker also follows sampler setup. A request `cache_prompt=false` or explicit slot does not bypass the global cache-selection block in this source. A different Go provider or HTTP client does not own a different backend KV slot. [Selection path](https://github.com/ggml-org/llama.cpp/blob/fc9ce6b9d52a8504edcb262abc92737c2289f96c/tools/server/server-context.cpp#L1775-L1788), [launch/sampler boundary](https://github.com/ggml-org/llama.cpp/blob/fc9ce6b9d52a8504edcb262abc92737c2289f96c/tools/server/server-context.cpp#L1911-L1960).

The package has no local PDB or COFF symbols. Exported get/set wrappers tail-jump into distinct verified function ranges, so nearest-export guessing is invalid. The diagnostic uses exact end-exclusive unwind ranges for those direct targets. It does not add model-name logic or binary addresses to production.

Static instruction inspection also identifies the cache allocator by references to its unique log messages, the prompt-save caller by its allocation/get-data call order, the checkpoint-list helper by its node construction, and the payload constructor by its three vector copies and verified CRT import thunks. `runtime-crt-cache-proof.json` records the exact ranges and supporting instructions. These are diagnostic labels for this installed binary, not source-symbol guesses or a production integration.

## One completed live observation

The production helper is invoked once by the existing test-only runtime harness. The actual portable profile/workspace resolves thinking on and effort **xhigh**. The request body SHA256 is identical to both original baselines: `e746786c4e5dd90f93761b927089a8afe071e9cd5d055ba39b22f2d46c12d0de`. History, transcript and instruction are identical. There is one Complete, one POST, no tools/retries, HTTP 200 and a normal stop. The response is a diagnostic artifact, not a replacement saved into the user's conversation.

| Observation | New task 223 |
| --- | ---: |
| Idle-slot LRU selection to launch | 1477.379 ms |
| Backend prompt evaluation | 8416.15 ms / 8180 tokens |
| Backend generation | 57587.74 ms / 1993 tokens |
| Client first exposed reasoning | 10248.193 ms |
| Client HTTP headers | 53.482 ms |
| Pre-launch pagefile-read bytes | 474574848 |
| Pre-launch all hard-fault bytes | 476872704 |
| Pre-launch hard faults | 7847 |
| ETW events lost | 0 |

This run evaluates the whole prompt; the historical run evaluated only four tokens. Cache state/runtime lifetime differs, even though input bytes and controls match. Therefore the shorter preparation is **not** credited to an optimization, and the different generation count is not a reasoning-setting change.

An independent review of the completed helper receipt confirms all nine named fixture facts/constraints, including current prohibitions on changing tests/catalogs, portable data and no publication. It verifies identical request-body hashes and actual summary instruction/transcript against their original helper inputs. This checks retention in the produced summary; it does not substitute for a subsequent main-agent continuation test.

Windows tracing is started only for this observation and stopped on model completion; no status/log/process polling is used. WPR temporary and final files stay in the diagnostic directory, with a unique owned instance. Numeric SSE instrumentation observes read/frame/payload timing without readahead, extra requests or raw streamed thought persistence in its telemetry. The existing helper receipt stores its ordinary summary as before. [WPR recording and instance options](https://learn.microsoft.com/en-us/windows-hardware/test/wpt/wpr-command-line-options).

The corrected ETW extraction attributes old-thread context-switch stacks through `BlockingStack()`; ordinary `CallStack()` belongs to the incoming thread. The initial extraction is superseded and is not used for off-CPU attribution. CPU samples, native intervals and hard-fault counts are unchanged by that correction. [TraceEvent CSwitch stack contract](https://github.com/microsoft/perfview/blob/v3.2.8/src/TraceEvent/TraceLog.cs#L1104-L1116).

Independent recounts agree with the corrected v2 extraction: constructor stacks occur in **390 CPU samples and 7805 switch-out observations**. The constructor-associated **1001 ms**, list-copy **1002 ms** and allocation **1004 ms** sums are nested observations and must not be added. All constructor intervals/samples belong to thread 24100. The recorded wait reason is `LpcReceive`; the off-CPU intervals include blocking and possibly scheduler-ready time, so this is not an exact pure-copy, pagefile-read or GPU-save timer. Same-process pagefile traffic alone cannot establish a particular interval's causal wait.

All-thread fault/off-CPU durations overlap and are not added as wall-clock phase costs. Missing stack observations are not proof that a phase did not execute. Runtime wall timestamps align ETW windows to milliseconds, while the native elapsed counter determines the 1477.379 ms interval. Independent native/event-clock alignment spreads are 1.394 ms for event-wall time and 5.533 ms for event arrival. Sampling cannot replace exact function-entry/exit instrumentation.

## Safe next direction and validation

The strongest new direction is **cache allocation/copying with pagefile activity**, rather than reducing the conversation or user reasoning. A universal backend candidate is immutable sharing of checkpoint payloads instead of repeated deep copies, with explicit memory budgeting for transient peaks. It requires measurement and ownership/concurrency tests in the inference runtime; this follow-up does not patch or replace that external engine. Disabling caching, shrinking checkpoints/context or automatically loading another instance is not a proven universal improvement from these observations.

Exact attribution of the historical-length pause needs another occurrence with phase timers around old-entry destruction, buffer allocation, checkpoint copy, state export/synchronization, restoration and sampler initialization. Forcing memory pressure or manufacturing many extra inference calls would produce a different workload and is not used here.

The updated numeric archival parser passes **6/6** synthetic privacy/grammar tests. The exact-range and time-window diagnostic passes **4/4** tests, including distinguishing nested checkpoint/allocation stacks from state-transfer stacks. The C# analyzer compiles with zero errors/warnings and is exercised against the completed real trace, including the corrected blocking-stack extraction. An initial capture attempt rejects malformed PowerShell arguments before any model request; its owned trace is stopped. After offline argument validation, the one actual inference and extraction complete successfully. There is no application-source change requiring another build or broad regression run.

Evidence lives in `.tmp/summary-cache-phase-diagnosis/`: `request-phases.json`, `preceding-lifecycle.json`, `runtime-metadata.json`, `upstream-provenance.json`, `source-runtime-audit.md`, `runtime-symbols.json`, `runtime-unwind.json`, `runtime-cache-exports.json`, `runtime-crt-cache-proof.json`, `independent-prelaunch-counts.json`, `independent-capture-review.md`, `independent-helper-controls-coverage.json`, and `provider-cache-contract-audit.md`. Live capture: `SuperCliCompact-33bdb4f8ed2f4bf5a51127ad586e1511/`; use its `analysis-corrected/phase-summary-v2.json`, capture/observer receipts and `model-receipts/runtime-2144441075/receipt.json`.

The canonical ETL and corrected numeric event stream are retained as gzip archives, round-trip verified against their uncompressed SHA256 hashes in `archive-manifest.json`. Together they occupy **163112645 bytes**, instead of **1316602090 bytes**. After independent review, the verified originals, regenerable ETLX index and superseded initial numeric extraction are removed; `cleanup-receipt.json` records their paths, sizes and hashes. Its small receipts remain for provenance. All new artifacts and dependency caches remain under the portable application directory.
