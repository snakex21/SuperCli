# Media, computer-use integration and performance review

## Base and reconciliation

This work began at `8506d67b9bf4acd41396f8792e8dc6ef0a5126ba` and was reconciled onto
`c5afaf85dde4637fd7820e32c4c4429992b20f8d` before final verification. The latter's
covered-window capture, bounded image analysis/cropping, GUI thumbnail service,
cache bounds and tests were retained. They are upstream work, not claimed as
new changes in this patch. A subsequent integration retained upstream
`d752fdae38d37dfd3528e17b1baf2239d63158ad` (headless QMP/WebDriver, process
ring buffer, Linux onboarding and TUI input changes). Its parallel incremental
history implementation was reconciled with this patch, keeping both sets of
regression tests. Final reconciliation additionally retained
`3eae0a55a7afa280c13add6fad37bd6d27980896`, including its image area-weight
precomputation, bounded thinking scan, compact discovery payload and cached session
date formatter. Its equivalent eight-style Marker implementation superseded our
implementation; both regression suites remain. Publication status is recorded
in the final delivery README.

## Implemented

- MCP native image blocks are decoded with aggregate size/count bounds and
  propagated through both bridge and direct registration paths. Multiple images
  share one follow-up message; text uses linear-time accumulation. Distinct
  structured MCP results survive alongside summaries; only equivalent complete
  JSON values are deduplicated, without rounding large integer values.
- Tool images use existing content-addressed session storage and one-request
  activation. SSE contains only portable session handles, never base64. GUI
  previews work live and after replay without browser-local attachment records;
  the TUI exposes saved paths. Session previews enforce the active workspace and
  reject traversal/symlink escapes, even when general agent access is broader.
- Explicit `generate_image` / `generate_video` tools implement OpenAI images and
  fal queue APIs, with real consent, bounded polling/downloads, cancellation,
  error handling and original output files. They have no default paid provider
  and remain discoverable instead of adding always-on schemas. Configuration is
  read only from the portable global config; project overrides cannot redirect
  credentials. See [setup](../media-generation.md).
- Configured MCP servers can enforce exact `allowed_tools` and per-call
  `confirm_calls`. Consent is an actual GUI/TUI response, not model-authored
  `confirm:true`. Headless calls fail closed when confirmation is required.
  No desktop-control server is installed or granted OS permissions automatically.
  See [computer-use setup](../computer-use-mcp.md).
- Older transcript pages prepend only new rows. Previously rendered images,
  expanded diagnostics and focus are retained, with scroll anchoring and
  cross-page tool/worker identity handling.
- `read_image` sniffs before payload allocation, uses a size-bounded exact buffer
  and cancellation checks, rejects non-regular files before opening, and retains
  all upstream crop/resolution behavior. A concurrently grown/shrunk file is
  rejected rather than reading past the observed size.
- The TUI marker retains the eight styles it actually uses rather than a second
  entire 39-style palette. Value-copy semantics and zero-value/no-color behavior
  are preserved; no transcript content, functionality or refresh interval was
  removed.
- Media tests compare filesystem identity rather than raw Windows short-path
  aliases. Cancellation tests warm lazy setup before their existing three-second
  synchronization deadline and drain the handler before closing its stores.

## Security review corrections

The initial review binary had a TUI consent defect: the ordinary question view
truncated action details while allowing an immediate affirmative Enter. Do not
use that superseded binary for generation or computer-control approval.

The corrected trusted action mode displays the complete scrollable details,
starts at Cancel, disables numeric quick-picks, and only enables explicit Tab
selection of Allow once at the end. Terminals smaller than 24×10 fail closed.
Scrolling back or shrinking the terminal clears an unsafe affirmative choice.
Terminal-control and bidirectional formatting characters are visible escapes.
New tests cover long generation prompts/MCP arguments at 80×24, 40×12 and 24×10,
queueing, cancellation, quick-picks, resizing and controls.

The new upstream native headless tool is also gated: an operator-owned global
`[headless.targets]` snapshot must allow the exact protocol, normalized endpoint
and action, including read-only actions. No target/action defaults or wildcards
exist, and project config cannot expand scope. Mutations additionally use the
same trusted per-action consent before dialing, target locking or profile
creation. Confirmation includes the session and complete arguments plus launch/
close effects and directory context. Existing QMP/WebDriver capabilities remain
available through explicit configuration; no actual user VM/browser was operated.

## Measurements

Go 1.26.2, Linux amd64, one Go worker (`GOMAXPROCS=1`, `-p=1`), AMD EPYC 9V74.
Medians of three runs from the original review stage before the final security
corrections/upstream integration. Upstream `d752fda` independently adopted
incremental history, and `3eae0a5` adopted the compact Marker, so those figures
are not new incremental gains over the final upstream base.
Allocated bytes are cumulative per operation, **not peak
resident RAM**. Earlier runs and interrupted build attempts are retained as
separate evidence; the following image measurements use the fresh `c5afaf85`
read implementation as the baseline.

| Workload | Before | After |
|---|---:|---:|
| MCP parser, 1,000 text blocks / 128 KB text | 9.33 ms; 67.54 MB allocated | 1.50 ms; 0.793 MB allocated |
| Complete, decodable 1920×1080 PNG, original detail | 1.659 ms; 14.915 MB allocated | 0.661 ms; 6.240 MB allocated |
| Reject 8 MiB non-image file | 2.118 ms; 17.072 MB allocated | 0.026 ms; 0.0086 MB allocated |
| TUI unchanged stream frame | 131,168 bytes allocated | 98,400 bytes allocated |
| TUI 180 stream events + unchanged ticks | 51.92 MB allocated | 40.12 MB allocated |
| GUI ten pages × 60 image results | 3,300 image-source assignments | 600 image-source assignments |
| GUI final image count | 600 | 600 |

The image benchmark builds a valid PNG and verifies it with the Go decoder before
measurement. The invalid-image workload is explicitly a rejection case. The MCP
baseline parser is identical at both upstream revisions. TUI timings improved
modestly with noise; allocation reduction is the stronger claim. GUI figures are
deterministic DOM construction counts, not measured network requests, browser
RSS or FPS. More detail: [paging report](2026-10-02-incremental-history-pages.md).

There is no measured claim about generated-token savings on a real paid model.
Generation uses concise typed calls and avoids script/base64 output; native image
bytes do not enter text history. Existing one-request pixel activation prevents
unrequested repeated image input. All model usage in the local smoke fixture is
synthetic and must not be treated as a cost or token benchmark.

Reproduce the performance checks:

```sh
GOMAXPROCS=1 go test -p=1 ./internal/tools/media ./internal/tools/mcp \
  -run '^$' -bench 'Benchmark(ReadImagePayload|MCPFragmentedText)' \
  -benchmem -benchtime=300ms -count=3
GOMAXPROCS=1 go test -p=1 ./internal/ui/tui -run '^$' \
  -bench 'BenchmarkTUI(UnchangedStreamFrame|StreamEvents)' -benchmem -count=3
node --test --test-concurrency=1 test/ui/*.test.cjs
```

## Verification and boundaries

Final command results and raw measurements are in
`supercli-media-perf-20261002/`. The Linux GUI HTTP/SSE smoke exercised the actual
executable, a local deterministic model endpoint, an actual stdio MCP subprocess,
consent, a complete PNG, a playable one-second MP4, byte-range video serving,
thumbnail serving, completion receipt and persistent session replay. It did not
invoke a paid generation provider. Real TUI startup/help/exit were exercised in a
pseudo-terminal.

The cloud browser refused localhost navigation (`ERR_BLOCKED_BY_CLIENT`), so no
native browser rendering, visual layout or Windows WebView2 result is claimed.
Windows artifacts are cross-builds; Windows runtime tests and native covered-
window capture need Windows CI/hardware verification. Real image/video generation
requires the user's enabled provider, key, model/CDN settings and per-call consent.
No such credentials were requested or used.

Startup pricing refresh is already asynchronous in upstream code. Its public
fixed-URL GETs carry no prompt, key or per-user model list. The QA executable used
a deterministic, fresh local pricing cache to avoid external fetches; no security
settings or network restrictions were bypassed.

The patched code does not silently enable new computer access. Stop/cancel can
stop local waiting, but an already-issued external desktop action may already
have happened; never blindly retry a consequential action.

The original review-stage Linux source verification passed: `go test ./...`, `go vet ./...`, all 139
Node UI tests, the 27-language README parity checker, and both executable builds.
The isolated reproducible HTTP smoke is available under `test/media-smoke/`.

Windows amd64 cross-builds also passed, including compilation of the media test
binary. PE inspection confirmed Windows GUI subsystem 2 for `supercli-web.exe`
and console subsystem 3 for `supercli.exe`. These checks do not execute Windows
tests and do not establish that remote Windows CI is green. Update archives
contain executables and documentation; reuse the existing portable data/skills
folder rather than treating them as a replacement full release bundle.

## Final security-corrected integration verification

After integrating `d752fda`, all Go packages passed again with `-count=1`,
`go vet ./...` passed, all **145** Node UI tests passed, and README parity passed.
Linux TUI/GUI builds and Windows amd64 TUI/GUI plus headless/media test executable
cross-builds passed. The final HTTP/SSE fixture again passed image, video range,
consent and durable replay checks. Independent read-only review found no open
code blockers in the consent, headless scope, MCP data or merged GUI paths.

The upstream Windows WebDriver profile test compared a short temp-root alias
with the sandbox's canonical long path. CI job 110877612133 recorded
`C:\Users\RUNNER~1` in the Go temp prefix and `C:\Users\runneradmin` in the profile.
The assertion now compares parent-directory identity with `os.SameFile`, retaining
all production sandbox restrictions and the outside-profile symlink rejection.

Two corrected PTY smoke runs passed startup, help, `/quit` and terminal-mode
restoration. Exit was observed within 0.119 seconds after the standalone quit
command in the timed run. Earlier harness failures are retained: one used an
unsupported command-line flag; another appended `/quit` to the still-populated
`/help` input. Clearing the input explicitly fixed that harness sequence without
increasing the five-second exit timeout or changing application code.

The review/build scripts enforced one Go worker, a 570 MiB managed heap target,
1000 MB group-RSS ceiling and 1500 MB own-cache ceiling. Final Windows build peak
observed group RSS was 663.50 MB; cache peak was 1259.31 MB. Native Windows runtime
and real browser rendering remain separate from these local checks; remote CI
status must be checked for the exact published commit.

After the final `3eae0a5` reconciliation, the complete Go test suite, vet,
**148** UI tests, README parity, Linux builds and HTTP/SSE/PTY executable smoke
passed again. The portable PTY script is now in `test/media-smoke/tui_smoke.py`;
two newest observations measured standalone quit at 0.118 seconds. A cold
regression attempt was safely stopped by the 1500 MB cache guard; clearing only
orphaned temporary files and redundant module ZIPs allowed the complete rerun
to pass with 1158.52 MB peak cache and 641.77 MB peak observed group RSS.
The same final tree's Windows TUI/GUI builds and headless/media test-binary
cross-compiles also passed (664.03 MB peak group RSS, 1179.27 MB peak own cache).

## Final upstream request-preparation reconciliation

Upstream `cb7f507e736ce1398e159e1c6ae08edada4cc151` was subsequently merged
without conflicts. Its registry revisions, tool-definition snapshot, exact
worker-report sharing, output restoration and completed-render reuse were
retained. Independent read-only integration review found no new scope or consent
regressions. The full Go suite, vet, all 151 UI tests, README parity, Linux
builds and HTTP/SSE/PTY smoke passed again (see `review-cb7-linux.*`). This cold
run used a temporarily approved 1700 MB own-cache ceiling; it peaked at 1451.31 MB
cache and 651.42 MB observed group RSS. Standalone PTY quit was observed at 0.120 s.
Windows TUI/GUI and headless/media test-binary cross-compilation passed on this
same integration: 654.58 MB peak group RSS, 1602.13 MB peak own cache, no guard stop.

The final controlled catch-up retained upstream
`0cc12b97dbc64af6f5b53a6904ca51be5e9c1cc8`, including native reasoning replay
and unchanged-viewport/search/menu optimizations. It merged without conflicts;
independent review found no changes to consent behavior or native image history.
The complete Go suite, vet, 151 UI tests, README parity, Linux builds and both
executable smoke paths passed again (`review-0cc-linux.*`), with 463.36 MB peak
group RSS and 1673.26 MB peak retained cache. PTY quit remained 0.120 seconds.
Windows TUI/GUI and headless/media test-binary builds passed on that same final
integration (206.53 MB peak group RSS, 1606.34 MB peak cache).
