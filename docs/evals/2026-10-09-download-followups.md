# Download follow-ups and tool handoff, 2026-10-09

## Observed failure

Read an actual recent Qwen conversation from the portable
session database. Its first GIF downloaded successfully (855,387 bytes). The
next human request asked for another GIF in a workspace subdirectory, represented here with a generic path:

> dzięki a możesz pobrać kolejny gif ale dać go tutaj? C:\projects\example\gify

The agent then made ten web lookups, no page read and no download. The turn took
76,639 ms and ended with reasoning rather than a completed action. There were
zero helper/worker calls. This destination was inside the project; the earlier
Downloads export fix did not address this failure. The narrow imperative
recognition lost the download/page contracts on the natural follow-up. Search
snippets also included binary GIF noise and repeated CDN queries.

## Changes

- Registered tools declare bounded follow-up tool metadata. A successful search
  makes page reading/downloading available; a successful page read makes
  downloading available. Handoff uses registered tool names, including calls
  through `invoke_tool`, and does not inspect page text for capabilities. It
  does not grant filesystem permission or add a model/discovery call. Contracts
  stay transient, preserve the stable system prefix, expire at the next Run,
  and participate in tool-definition caching/token estimates.
- Natural download requests and directory/count-only continuations retain
  task context across fresh GUI loops and TUI resume. Only bounded raw human
  transcript rows supply previous destination authorization. Provider context,
  repository addons, summaries, assistant output and tool results cannot grant
  export access. Unrelated work and revocation stop inherited destinations;
  explicitly changing destination replaces the previous target.
  An explicit filename grants only that exact file; its parent, siblings and
  children remain outside the grant. Canonical parents are pinned and checked
  again before publishing. Named folders with dots remain folder targets.
- Page-reader media mode returns the page title and at most four declared
  Open Graph/Twitter, download-anchor and audio/video asset URLs from a bounded
  prefix. Ordinary PDF/ZIP links and `download` attributes work without page
  image metadata, on arbitrary hosts. Signed relative URLs are preserved.
  Normal text mode retains compact declared links too. Media mode omits unrelated page
  prose and makes one guarded HTTP request. It guesses neither media IDs nor
  site-specific download URLs. Ordinary text mode remains available. Binary
  search snippets are omitted; returned URLs are preserved exactly.
- Independent requested downloads can execute concurrently, including exports
  outside the project. Conflicting targets stay ordered, and other filesystem
  tools cannot reuse a downloader grant. Tool completion remains a single
  barrier with cancellation and ordered results.
- Self-contained public web workers skip unnecessary repository collection.
  Simple foreground web tasks retain the same shortcut across continuations.
  Mixed project/command work keeps normal collection; no additional worker is
  forced merely to download a file.
- Public-task recognition uses generic resource/source relationships and web
  signals, with no named-service branches. URL queries remain legitimate search
  queries, and results label them as search evidence; they are never silently
  redirected into a page fetch.

## Real GUI conversation

Opt-in `TestDownloadFollowupLiveGUI` exercised three actual `/api/chat` requests
in the same persisted conversation. Each request constructed a new production
Loop. Local Qwen `qwen3.8-27b-uncensored` used thinking enabled with effort `max`;
normal adaptive delegation and automatic navigation/preflight remained enabled.
The model received no fixture URL, chosen media ID or fabricated tool result.
Application state stayed under `.tmp/gif-followup-live`.

| Turn | Request | Valid GIF files | Tools | Main calls | Wall time |
| --- | --- | ---: | ---: | ---: | ---: |
| 1 | New GIF to actual Windows Downloads | 1 | 2 | 3 | 62.634 s |
| 2 | Natural “możesz pobrać kolejny” to workspace/gify | 1 | 4 | 5 | 38.065 s |
| 3 | Directory/count-only continuation, two different cat GIFs to a new external folder | 2 | 5 | 4 | 37.607 s |

All three completed without tool failures, helper calls or delegated tasks.
The last turn batched two page reads and two independent downloads. SHA256
values differed for its two files. Decoding verified frame counts 16, 26, 80
and 1; the last item is a valid one-frame GIF, so this test does not establish
that every selected asset is animated or assess subjective relevance.

Tools occupied about 1.55, 2.28 and 2.07 seconds respectively. Most remaining
time was model backend wait and generated text/reasoning. There were still
redundant URL-as-search-query choices; the implementation does not force model
decisions, lower the user's reasoning setting or promise a general speedup.
The failed historical turn and the new successful follow-up are distinct
requests, not a controlled A/B benchmark or a reliability estimate.

Raw evidence: `.tmp/gif-followup-live/run-2654366879/receipt.json` (full durable
messages, summaries, request sizes/tool names, file sizes/hashes and decoded
dimensions). The live test is opt-in and skipped by the ordinary suite.

## Failure found by repeating the evaluation

A second candidate run (`run-1708761582/receipt.json`) did not complete its
first turn: 17 tools, 12 main calls, four fetch errors, 201.694 seconds. The GIF
was saved in the last step, but the safety ceiling fired before a final answer.
The model guessed an unavailable page; its raw HTML error exposed a bootstrap
script, which led to decoding that script and trying guessed API endpoints.
This failed candidate remains in the evidence; one successful conversation is
not proof of universal reliability.

HTTP HTML errors now return a bounded title/readable summary instead of raw
HTML, hidden attributes, scripts or bootstrap data. Plain text and JSON API
diagnostics retain their existing useful body excerpts. The implementation is
shared across hosts and modes. Transport regressions cover truncated/unclosed
scripts, misleading MIME types, nested inert elements, one HTTP request,
response-body closure and preservation of ordinary API errors.

The live acceptance criterion requires valid requested files, no terminal
error and an actual final assistant answer. A recovered 404 is legitimate;
reasoning-only output or reaching the safety ceiling cannot pass.

The subsequent real GUI conversation (`run-3997544857/receipt.json`) completed
all three turns with zero tool failures, helper calls or delegated tasks:

| Turn | Valid GIF files | Tools | Main calls | Wall time | Tool time |
| --- | ---: | ---: | ---: | ---: | ---: |
| 1 | 1 | 3 | 4 | 125.560 s | 1.933 s |
| 2 | 1 | 3 | 4 | 119.710 s | 2.453 s |
| 3 | 2 | 8 | 7 | 202.905 s | 3.112 s |

The two-file turn batched its downloads but still repeated an identical search.
Decoded frame counts were 1, 19, 80 and 1. All actual final answers were present;
the two final assets had different SHA256 values. Model streaming/reasoning took
96.282, 88.622 and 162.639 seconds, with backend wait a further 27.104, 27.913 and
37.034 seconds. This later run was slower in elapsed time despite fewer tools
in the natural follow-up: do not report an overall speed improvement from it.
The generic anchor/audio/video extraction extension was then covered by
arbitrary-host transport fixtures and the full affected-package suite; the
live conversation above used the preceding metadata-reader candidate.

## Validation and installation

Deterministic regressions cover natural follow-ups, raw-history/projection
separation, negative/source/excluded paths, destination replacement, same-batch
discovery/dispatch handoff, immutable registry metadata, cache invalidation,
permission boundaries, concurrent download completion and cancellation during
history reads. Full affected-package and interface results and the executable
installation hashes are recorded under `.tmp/gif-followup-fix`; previous
executables receive verified local backups. Existing settings and sessions are
preserved.

Full checks passed for agent, tools/core/web/sandbox/ctxexec, session storage,
TUI, WebGUI, app and checkpoint (186.452 seconds standalone package allowance).
After the final generic page-link changes, affected agent/web/TUI/WebGUI/app
tests and vet passed again, followed by both executable builds. All 235
JavaScript UI tests passed. Release checks are
`.tmp/optimization-oct8-2026/go-gif-followup-release-{0,1,2,3}.log`; full earlier
checks are `go-gif-followup-final-{0,1}.log` in that same directory.
