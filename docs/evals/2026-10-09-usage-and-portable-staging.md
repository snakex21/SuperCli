# Usage projection and portable staging, 2026-10-09

The Usage dashboard used a full transcript-telemetry query to aggregate up to 2,000 recent turns. It decoded file-change lists and checkpoint bindings that summarizeTelemetry never consumed. A separate ReadRecentTurnTelemetry projection now selects the same window, counters, phases and diagnostics but leaves those three transcript-only fields empty. Full transcript and checkpoint recovery APIs retain their payloads and ownership checks.

Three matched benchmark samples on Windows/AMD Ryzen 7 5800X3D, GOMAXPROCS=2:

| Synthetic 2,000-row fixture | Full query, median | Projection, median | Allocated bytes/full | Allocated bytes/projection |
| --- | ---: | ---: | ---: | ---: |
| Ordinary, no file changes | 15.80 ms | 14.36 ms | 8,100,504 | 7,670,474 |
| 64 file changes per row | 133.38 ms | 21.06 ms | 43,916,954 | 7,790,906 |
| Same changes + resolved bindings | 129.41 ms | 21.83 ms | 45,634,120 | 7,799,014 |

The real portable database contained only 80 turns in the bounded seven-day window: file_changes_json total 52,449 bytes, phases_json 14,744 bytes, tool_diag_json 6,442 bytes. It lacked the new binding column. The large synthetic fixtures establish scaling; they do not describe current startup RSS or claim faster model generation.

The audit also found active export/document/backup/Thunderbird paths writing temporary data outside the portable folder. Web export now stages under DataDir/.supercli/staging, excluded by the existing explicit export allow-list. Document and backup import use MultipartReader directly into private, per-request portable directories, with cleanup on success and failure. No process-global TMP/TEMP change, OS-temp fallback, background polling or model instruction is added. Only the highest-priority candidate is retained; file/document/files precedence and first-file behavior remain intact. Whole-request and selected-file byte limits remain, and the old default limits of 1,000 parts and 10,000 header values are explicitly enforced.

For isolated staging of a synthetic 5 MiB DOCX upload, median allocations fell from 16,863,072 bytes (93 allocations) to 47,993 bytes (74 allocations). Median staging wall time was 15.37 ms versus 14.69 ms, a small difference dominated by local filesystem noise; the demonstrated benefit is avoided buffering and double staging. Document extraction and response encoding are common costs excluded from this benchmark.

Regression tests first failed against the previous export/document/backup implementation with OS temp deliberately blocked, then passed with portable staging. Tests cover a valid DOCX, a file above the old memory-spill threshold, a large backup, malformed/unsupported documents, cleanup, unwritable/empty roots, field precedence including an oversized unselected file in both orders, part/header budgets and ignored-body byte limits. Telemetry tests compare all consumed fields, ordering, limits, window and cancellation, plus the actual Engine dashboard aggregation.

Evidence and completion/build receipts are stored locally under .tmp/optimization-oct8-2026/closure-audit. Final integration coverage and binary hashes are added after source review.

Small TXT/Markdown uploads now stage to a private portable file instead of retaining the body in multipart memory. This is a deliberate portable-storage tradeoff; no claim that every small import gets faster. Thunderbird attachment and MSG conversion/import staging now resolve the explicit configured data root, keep the bridge root pinned, and fail without OS-temp fallback when unwritable. Mail construction remains lazy.

The parallel dispatcher removes the buffered result channel and writes each indexed result slot from its own goroutine. The WaitGroup still drains all started calls before result/history/event processing in input order, including fatal/canceled work. Matched real dispatcher benchmark medians for 2/8/32 calls were 10.387/24.259/65.240 microseconds before and 9.010/20.536/62.635 after, with two fewer allocations per batch. This does not reduce LLM calls or promise GPU throughput.

## Real CLI Tenor task

A plain Polish request asked the installed batch CLI and the already loaded LM Studio Qwen3.8-27B to download an actual GIF from https://tenor.com/view/hello-waving-wave-emoji-emoticon-gif-15592704 into hello.gif. Each run used separate portable workspace/data, thinking off, the same prompt and a 12-step bound. User configuration and models were not restarted or changed.

Baseline k exhausted all 12 model requests without a file: web_fetch discarded the declared asset URL, the model guessed a nonexistent CDN URL (404), then repeated lookup. The staged implementation preserves at most four bounded page-declared media URLs using the existing fetch response, without guessing provider paths or adding network calls or model instructions. A retest completed with exit 0 in 7 model requests/26.1 s and saved 254,803 bytes, GIF89a, 498x498, SHA256 b4d986909b3df2bb41205cf6a8a87754f1905ce79b68c7a3532b0efd9e8b02b8. Download finished in request 4. The model still submitted one invalid fetch argument and tried unavailable Unix file before correcting itself with PowerShell. One case is not a success-rate estimate; comparing the 43.8 s failed baseline to the successful retest is not a throughput benchmark.

Review then identified fake metatags/closing-head text inside comments and scripts plus avoidable lowercasing copies. A bounded tag scanner now skips comments and raw text, stops at real head/body boundaries, caps metadata scanning at 64 KiB/128 tags and validates resolved asset URLs with existing rules. Regression tests cover comment/script/style false metadata, unclosed blocks, unknown Content-Type, URL schemes/private addresses/userinfo, size/output bounds and one-request fetching. For the 120 KiB synthetic ordinary page, final metadata extraction median is 0.204 microseconds with zero allocations; a >2 MiB page with unknown Content-Type is 0.211 microseconds/zero allocations; declared two-asset metadata is 5.498 microseconds/2,672 allocated bytes. These are extractor costs, not model latency.

Fresh final integration tests and vet passed for eight packages: storage/session, webgui, app, ui/tui, agent, tools/mail, tools and tools/web. Substituting the saved original 15 sources and omitting 10 added sources reconstructs the exact validated k source hash; unrelated package and unchanged frontend checks are inherited from that source-proven snapshot. Source hash before release compilation: 78d088515e517e26c757329f8e33dba26f6f7036911cc95690cebe496711a58c.

The final raw-script scanner follows the escaped/double-escaped closing behavior described by [HTML tokenizer states 13.2.5.18–31](https://html.spec.whatwg.org/multipage/parsing.html#script-data-state). A regression fixture covers a fake metatag after a double-escaped closing string; another contains 6,000 near-matching script end names. The latter completes in a median 139.6 microseconds rather than repeatedly scanning the entire remaining tail. Final web tests and vet were repeated after these two web-only source changes; the seven unchanged packages retain the fresh integration evidence above. The downloaded GIF fully decodes into four frames, 15 hundredths of a second each, with a continuous loop.

Final declaration tests additionally reject metatag-looking text in inert CDATA/bogus comments/processing instructions and quoted DOCTYPE fields. No fetch/network/model/schema behavior was added for these cases.

## Completed installation and final live test

All ten TUI/GUI builds completed for Windows amd64, Linux amd64/arm64 and macOS amd64/arm64. Source SHA256 78d088515e517e26c757329f8e33dba26f6f7036911cc95690cebe496711a58c. Local EXEs were atomically replaced with backups; help returned exit 0 for both. supercli.exe: 26,931,200 bytes, SHA256 52c45b314e520ea52511c57a8cccc6b0da6d5fcaf37501671fbcfd41ab7daf72. supercli-web.exe: 23,863,296 bytes, SHA256 e4ca93ca261904ffec868bedaf5295bf8ee7d07481bfa7c38ce5cf0610ee5379.

The identical Tenor request on this final installed CLI succeeded again: exit 0, 19.98 seconds, six model requests; the file was downloaded during request 3. The resulting GIF has the same 254,803-byte hash as the fully decoded four-frame animation. One unnecessary Unix file attempt still failed and was corrected with PowerShell. The final trace is qwen-final-receipt.json. Model nondeterminism, provider/network timing and cache state mean this repeated success is functional evidence rather than a latency A/B. The unchanged OpenCode Zen routing is outside this modification.
