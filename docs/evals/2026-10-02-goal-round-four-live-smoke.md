# Round four: live provider smoke, 2026-10-02

This is a correctness smoke of the current SuperCli provider and agent Loop code, not a baseline/current performance comparison. No production code was changed for this run. The helper was built directly from current sources; it did not use or lock the installed EXE.

## Scope and isolation

- Fixture and all helper binaries, logs, caches and measurements live under `.tmp/runtime-live-2026-10-02-round4/`; existing portable Go caches were reused.
- Separate in-memory Loops; no session writer, user configuration writes, project history, repository modifications, model loading or installation.
- The real `read_many` implementation and automatically registered `read_output` were the small toolset. Two two-line fixture files were requested in reverse filename order so order preservation was observable.
- A short Polish greeting and a read-only file-content question ran sequentially, with one inference request at a time, a 192-token output cap per call, thinking disabled and effort `none` only inside the helper process.
- Each request had a 35-second deadline; each case a 60-second deadline. No screenshots, vision input, large history or substantial GPU prefill were used.
- No raw session prompts, answers, tool output or credentials appear in the measurements. Synthetic fixture prompts remain in the reproducible helper source.

## Availability and models

LM Studio `/v1/models` and `/api/v0/models` were queried once each. The loaded model was `qwen3.8-27b-uncensored`. Existing configured Zen and Kilo `/models` endpoints both returned HTTP 200 and listed the configured IDs.

| Backend | Exact model | Construction |
| --- | --- | --- |
| LM Studio | `qwen3.8-27b-uncensored` | Real `NewOpenAI`, loopback `127.0.0.1:1234/v1` |
| OpenCode Zen | `space-bunny-free` | Real `NewOpencode`, existing Zen URL and unchanged public-auth branch |
| Kilo | `poolside/laguna-xs-2.1:free` | Real `NewOpenAI`, existing configured Kilo URL, no configured API key |

## Observed complete cases

Times are single observations. First text is the first user-facing MessageEvent relative to case start; completion includes both calls and the tool for the file question. Token usage is provider-reported and aggregated over the case, not an estimate of distinct history tokens.

| Backend/case | Model calls | Successful tools | First text | Complete | Input/output | Cached input |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Qwen greeting | 1 | 0 | 2.835 s | 3.056 s | 593 / 7 | 0 reported |
| Qwen read_many | 2 | 1 | 3.184 s | 3.502 s | 1387 / 48 | 0 reported |
| Zen greeting | 1 | 0 | 1.205 s | 1.257 s | 765 / 8 | 128 |
| Zen read_many | 2 | 1 | 3.160 s | 3.476 s | 1725 / 49 | 848 |

Both greetings returned text without calling tools. Both file questions used exactly one `read_many`, read both files successfully in the requested order and returned both exact fixture values in that order. There were no tool failures, retry notices or output truncations in Qwen/Zen. Each file case followed tool-call then final-answer completion. Every instrumented Qwen call had one HTTP request and HTTP 200. The unchanged Zen wrapper owns its transport, so HTTP counts/status were not separately instrumented; normalized metrics use null rather than claiming zero requests.

## Kilo limitation and fixture correction

Kilo was **not a complete successful smoke**. The greeting received HTTP 429, produced no text and had no token usage. A subsequent file case received HTTP 200 on its initial tool call (444 input / 29 output tokens), executed one successful ordered `read_many`, then received HTTP 429 on two subsequent model calls. No final answer was produced.

Exactly **four wire requests** were sent to Kilo in total: 429 for greeting, 200 for the tool call, then 429 and 429. The initial helper cancelled only the provider child context when a retry notice arrived, without forwarding a terminal error to the Loop. The empty stream could therefore appear completed or allow the Loop to advance. This was a fixture flaw: the helper did prevent transport-level retries but did not stop the whole case. It also let the later case begin after the failed greeting. No further live requests were sent after identifying this flaw, including to Qwen or Zen.

The helper now forwards terminal cancellation on the first notice and stops subsequent cases after ErrorEvent. Deterministic `TestFixtureStopsOnFirstRetryNotice` passed offline: one mocked provider call, one ErrorEvent, zero DoneEvents. The corrected helper compiled, but was deliberately not rerun live. Original source and raw metrics remain in the fixture; `redacted-summary.json` documents the flaw and unknown Zen HTTP counters. This observation does not establish a production cancellation bug: production cancellation paths were not modified or asserted to behave the same way.

## Reproduction and limits

Helper: `.tmp/runtime-live-2026-10-02-round4/runner/main.go`; original measured version: `main-initial.go.txt`. Per-provider raw metrics are under `fixture/`; normalized redacted metrics are `redacted-summary.json`; availability and offline fail-fast results are alongside it. Rebuild the helper with `go build -o .tmp/runtime-live-2026-10-02-round4/smoke-fixed.exe ./.tmp/runtime-live-2026-10-02-round4/runner`. Offline guard: `go test ./.tmp/runtime-live-2026-10-02-round4/runner -run TestFixtureStopsOnFirstRetryNotice -count=1`. Cache/temp environment must remain repo-local as in the parent evaluation setup.

This custom Loop uses a deliberately small toolset and empty history. It does not measure the native GUI/TUI, WebView2 RSS, FPS, input latency, long-session correctness, vision, or the full application startup path. There was no live baseline/current A/B and no claim that microsecond whitespace improvements explain these second-scale timings. The smoke confirms live provider/tool ordering behavior on Qwen and Zen; Kilo remains rate-limit blocked for complete validation.
