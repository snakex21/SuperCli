# Qwen recovery after the reported reload

The four most recent completed native LM Studio records available in the bounded snapshot independently support the user's observation that performance recovered after unloading and reloading Qwen. Native generation measured **34.11–35.14 tokens/s**, compared with **28.05 tokens/s** in the prior R7 archived greeting replay. Native prompt evaluation measured **717.70–934.90 tokens/s**, compared with **277.50 tokens/s** in that replay. This is a recovery observation, not a matched before/after benchmark or an attribution to the R9 SuperCli changes.

## Scope and evidence

Read-only inspection used one finite 2 MiB snapshot of the existing LM Studio October 2 server log, one GET of local model metadata, and one bounded query of four completed Qwen usage records from the portable SuperCli database. No new generation POST, image upload, capture, command execution, warmup, retry, model reload, KV reset, setting change, or log refresh was performed. Request bodies and reasoning text are absent from the saved receipts and this report.

Model metadata still identified `qwen3.8-27b-uncensored`, architecture `qwen35`, VLM type, quantization `Q4_K_M`, loaded context length **100,608**, and maximum context length **262,144**. Those exposed fields match the R7 model metadata; they do not identify every GPU backend, KV quantization, sampling, or runtime state. The API snapshot establishes the currently loaded model, not an independently measured reload time. “After reload” refers to the user's reported intervention and the subsequent finished records.

## Completed records

Timestamps are local Europe/Warsaw on October 2, 2026. Total input/output and TTFT come from persisted completed usage; evaluated input, prompt duration, and decode speed come from native finished timing records. Outputs include reasoning and ordinary generated tokens. All four logged requests were text-only with zero image parts.

| Finished | Native tools | Total input | Native evaluated input | Output | Reasoning | Native prompt ms | Native prefill tok/s | Native decode tok/s | Stored client TTFT ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 23:35:08 | 3 | 1,676 | 516 | 95 | 69 | 718.97 | 717.70 | 35.10 | 1,387 |
| 23:36:25 | 14 | 5,875 | 5,875 | 1,998 | 1,749 | 6,284.08 | 934.90 | 35.14 | 6,562 |
| 23:37:23 | 14 | 8,207 | 3,950 | 1,847 | 1,644 | 4,488.45 | 880.04 | 34.87 | 4,524 |
| 23:38:06 | 14 | 10,236 | 2,860 | 1,352 | 871 | 3,457.74 | 827.13 | 34.11 | 3,501 |

Native output counts match the completed usage counts. Native slot release reports were untruncated. Each body carried `cache_prompt:true`. The API did **not** report an authoritative cached-input count: all four usage records have `has_cached_input=0`, so their stored cached count of zero means **unavailable**, not proof of a cold request or cache miss. Total input minus native evaluated input is respectively **1,160 / 0 / 4,257 / 7,376** tokens. This is an implied unevaluated/reused amount inferred from two receipts, not a provider-reported cache metric.

The stored prefill profiler has no native timing feed in these receipts: it records all input as estimated evaluated input when cache usage is unavailable. Consequently its inferred rates differ from the authoritative native rates above; they must not be used to claim native prefill speed. TTFT measures client first model output and can include overhead beyond native prompt evaluation.

## Prior reference and limits

The R7 historical-body replay finished on October 2 at approximately 22:16 local time: input **1,683**, native evaluated input **1,032**, output **84**, native prompt time **3,718.93 ms**, prefill **277.50 tok/s**, generation **28.05 tok/s**, and client first output **4,163 ms**. It replayed the older three-tool text-only greeting with a 256-token output cap; its outgoing wire SHA-256 was `04fb0d99d02bdbfed0b6f77016fa9a71e24951dde8ac84adfe439603dcf61acf`. This earlier request was partly reused, so it is not a cold baseline.

No exact archived-body replay was needed or performed for this observation. The current first record is another three-tool, zero-image request, but input **1,676** and output **95** differ from the archived replay. The other three have different histories, fourteen native tools, much longer outputs, and different reuse. Current exact wire bytes/hash were not captured. The ignored receipts retain hashes of parsed-and-reserialized server log bodies for association only; those hashes are not HTTP wire hashes and cannot establish exact wire parity. No identical-payload or cold/warm speedup ratio is claimed.

These records demonstrate that native decoding and prompt evaluation recovered toward the previously observed mid-30-token/s generation performance; they do not establish that every request reaches exactly 36 tokens/s. The original slowdown's cause remains unresolved. Reload correlation does not identify a GPU, KV, backend, model, or SuperCli regression. The R9 file-image encoding optimization preserves request bytes and cannot explain this text-only recovery; no R9 performance attribution is made.

## Reproducible receipts

Portable ignored evidence is under `.tmp/goal-qwen-reload-round9-2026-10-02/`: `completed-native-records.redacted.json`, `completed-usage.redacted.json`, `loaded-model.redacted.json`, and `source-receipt.redacted.json`. Native tasks were matched to the preceding request by log character position, avoiding the same-second timestamp collision between completion of one request and start of the next. The prior reference is `.tmp/goal-runtime-round7-2026-10-02/live-native-timings.redacted.json` and `live-results.redacted.json`. No production source changed during this verification.
