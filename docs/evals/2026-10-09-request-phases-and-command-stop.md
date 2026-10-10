# Request phases and command Stop regression

Date: 2026-10-09. This iteration measures the production chat handler, provider construction/encoding/HTTP, first consumed model output, SSE flush, terminal frame and durable completion receipt. It also exercises the production browser chat JavaScript over real TCP HTTP with a native curl process.

## Local Qwen measurement

Two short foreground turns on the already loaded qwen3.8-27b-uncensored at loopback LM Studio. No reload, background inference, external service or user conversation was used. Thinking was disabled only in the isolated probe process. The fixture sets navigator=off and preflight_repo=false: it measures the coordinator registry/prompt/session/memory path without another classifier call. This is not a measurement of every user conversation profile.

| Stage | First new fixture session | Following turn |
| --- | ---: | ---: |
| Handler admission before SSE headers | 12.25 ms | below clock resolution |
| Session state after headers | 20.56 ms | 0.53 ms |
| Remaining preparation before provider entry | 36.00 ms | 11.76 ms |
| Provider entry to HTTP request start | 1.00 ms | below clock resolution |
| HTTP request start to first model output | 4545.62 ms | 431.38 ms |
| First output to finishing | 35.59 ms | 33.52 ms |
| Finishing to done SSE | 1.05 ms | below clock resolution |
| Done SSE to handler return | 11.69 ms | 3.00 ms |
| Submit to durable receipt | 4664.28 ms | 480.19 ms |

Each turn used exactly one real request, 14 tool definitions, 15,578 / 15,681 request bytes and 4,206 / 4,231 reported input tokens. Output was two reported tokens. There was no hidden helper request or repeated prompt.

First HTTP response bytes arrived about 11 / 4 ms after writing the request; that includes response headers and is not a model token. First model output is measured separately. Provider wait includes LM Studio queueing, prefill and first token generation; the OpenAI-compatible response does not split those model internals. Do not call this pure prompt-processing time.

Cold versus warm here is one fixture pair, not a before/after speedup attributed to a code change. LM Studio reported cached_input=0; the large TTFT difference alone does not establish the amount of reused model KV cache.

## Local component benchmarks

Three samples, 150 ms each, GOMAXPROCS=2; no model request. Median production helper costs:

| Component | Median time | Bytes allocated/op | Allocations/op |
| --- | ---: | ---: | ---: |
| session_state_warm | 0.618 ms | 28,022 | 306 |
| registry_prompt_memory_loop_warm | 4.676 ms | 894,792 | 10605 |
| session_recall_warm | 0.521 ms | 6,040 | 108 |

The loop helper includes registry, prompt, goal and memory wiring. These independent microbenchmarks are not additive slices of the live request. Cold handler admission also lazily opens the global memory store, and session preparation lazily opens/migrates SQLite. Those remain measurable candidates; no unbounded cache was added.

## Stop after a native command

The new integration launches a native curl against an owned loopback endpoint, waits for an actual request event, dispatches Stop through the production JS DOM handler, and submits the next message before the prior durable receipt. Completion is awaited through events, not progress polling. Canonical persistence and recovered session identity are asserted.

- Running child: Stop to next request about 440 ms; next test-provider message about 453 ms. The owned server observes the curl connection close.
- Store gate held before launch: the gate stays held throughout Stop and the next response. After the fix, next request starts at about 9 ms and next message appears at about 19 ms. No curl child is launched in this variant.
- With exact previous checkpoint source supplied by Go overlay, the same held-gate GUI case fails the 20-second test bound. That establishes a real cancellation regression rather than only a visual timer issue.

These numbers are local fixtures using a deterministic provider. They do not measure browser paint/compositor latency or the real Qwen next-response time.

## Fixes and safety

An explicit positive command timeout now starts before checkpoint admission/BEFORE and Runner preserves the earlier deadline. Zero/omitted still means no whole-command timer. Manager/Turn waits used by command admission react to cancellation; the local locks add about 26 ns per uncontended pair with zero per-operation allocations. Deferred completion seals first and gets its stable owner identity without waiting for a worker-held Turn lock. A provably RAM-only canceled owner needs no gated native-store cleanup; uncertain refs and actual leases still use guarded cleanup.

AFTER snapshots, undo/redo and accepted-work barriers remain. Stop does not authorize a new mutation across unfinished durable work, and curl is not falsely classified as read-only.

## Evidence and scope

Opt-in probe: internal/webgui/chat_phase_probe_test.go; native HTTP/DOM tests: internal/webgui/chat_stop_command_http_test.go and scripts/test-chat-stop-http.cjs. Data and temporary files are under the repository .tmp directory.

Completed logs and JSON receipts under .tmp/optimization-oct8-2026/closure-audit and its parent include live-qwen-phase-measures-o.json, synthetic-phase-measures-o.json, component-phase-measures-o.json, go-stop-integration-oct9-o-0.log and go-gui-held-checkpoint-baseline-oct9-o-0.log.

The full GUI fixture suite passes 235 tests with no skips. It covers Stop/queue handoff, error/EOF/completion, reasoning ordering, lazy history/media, exact text recovery and retained cache ownership. DOM fixture tests do not simulate WebView2 layout/paint/RSS.

Final validation passes all 15 changed/dependent packages and go vet, with 235/235 UI tests and no UI skips. Ten target builds (TUI+GUI for Windows amd64, Linux amd64/arm64, macOS amd64/arm64) were verified; both Windows EXEs are installed and pass --help. Exact source/binary hashes and backups are recorded in .tmp/optimization-oct8-2026/final-oct9-o.json.
