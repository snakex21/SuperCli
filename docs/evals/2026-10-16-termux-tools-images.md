# Termux onboarding, tool completion and image replay

Changes started on 2026-10-15 and verified on 2026-10-16. No release tag or
commit is created by this repair. Data and validation artifacts stay in the
portable application tree.

## Repairs

- **OpenCode Zen setup:** connection verification now exercises the harmless
  tool-bearing request shape accepted by the free gateway. Previously setup
  used a tool-less request, received `403 FreeTierError`, and rejected a model
  that worked during a normal agent turn. Other providers retain their existing
  verification path. Verification never executes the test tool.
- **Busy TUI input:** typing `/` opens and filters the command menu while a run
  is active. Slash commands stay out of the model queue; local commands preserve
  cancellation ownership and unsafe changes remain blocked during work. File
  mentions preserve the rest of the draft. `/usage` on a non-Codex provider
  opens general usage/cost information instead of only a subscription warning.
- **Tool protocol:** Responses identity, arguments and completion events are
  accumulated into one call, including partial and terminal-only events.
  Conflicting parallel identities are rejected instead of guessed. Exact known
  tool names wrapped in `functions.` / `tools.` can be normalized without
  changing arguments or selecting a fuzzy target. Malformed-call feedback
  explains that `invalid_tool_call` is an error marker, not a tool to discover.
- **Async delegation:** the parent waits for its own pending worker reports and
  automatically continues. Waiting uses notifications and remains cancellable;
  workers from a cancelled/other run cannot wake a later run. Separate report
  publication and acceptance counters prevent an already consumed report from
  causing an extra model request. The async acknowledgment explains how to wait,
  discourages restarting pending work, and distinguishes worker IDs from
  `read_output` handles. This guidance applies to every model/provider.
  Explicit paired `DelegatedWaitEvent` events identify the interval where the
  host is waiting rather than asking the main model. The GUI pauses its model
  inactivity watchdog only for that interval and restores it before continuing;
  merely having an active worker does not disable the parent watchdog.
  Terminal `DoneEvent` / `ErrorEvent` also disarm the model timer while the host
  finishes durable checkpoint/telemetry writes.
- **Anthropic images:** missing dynamic vision metadata is treated as unknown,
  not proof of text-only support. Explicit text-only metadata/user overrides
  and actual endpoint rejection remain respected. All results in a parallel or
  multi-wave tool batch precede auxiliary image messages, preserving every
  `tool_use` / `tool_result` pair through provider normalization and persistence.
- **Reopened GUI sessions:** host image carriers are rendered with their tool
  result, not as an additional user message. Old unmarked carriers are projected
  only when their exact envelope, host image name, session-media reference and a
  unique adjacent completed tool call agree. Genuine attachment records take
  precedence. A bounded preceding-page read recovers missing page-boundary
  context only when needed. Stored conversation text, sequence numbers and model
  history are not rewritten. Tagged carriers also remain internal when preview
  storage is unavailable.
- **Checkpoint deadline evidence:** native Git can exit with an ordinary error
  while its context is expiring. Git initialization, snapshot/ref commands and
  finalization now preserve both the context cause and native error. A cancelled
  `rev-parse` with exit code 1 is not mistaken for an absent ref. Mutation
  admission remains closed on failure. A regression checks cancellation,
  deadline and ordinary native failure without weakening deadline assertions.

No user thinking level is changed by these repairs.

## Evidence

- Before the Zen setup fix, a live public Muse Spark free-model probe reproduced
  the setup `403` while its normal tool call succeeded. After the fix the same
  verification succeeded.
- A live isolated Muse coordinator plus **two actual async model workers** read
  an in-memory fixture, returned reports and recorded a combined review once.
  Result: 3 coordinator tool calls, 2 worker fixture reads, 0 errors, completion
  without manual continuation; one sample took 17.094 seconds. This is a small
  workflow check, not a general speed benchmark.
- A later repeated live sample exposed model-level redundant delegation: it
  started an identical third review under a new call ID, then tried using a
  worker ID with `read_output`. The host had not duplicated a streamed call.
  After adding actionable continuation guidance to the async acknowledgment,
  the same unchanged test task passed with 2 actual reviewers, 1 combined record,
  3 coordinator tool calls and 0 errors in 14.038 seconds. This does not guarantee
  that a probabilistic model will never repeat work on another task. Two further
  consecutive repetitions also passed the same exact worker/call/error checks.
- Anthropic HTTP fixtures inspect the actual encoded PNG bytes. Coverage
  includes user attachments, tool images, dynamic metadata, durable and inline
  images, one/two tools, subsequent turns without old pixel retransmission,
  scheduling waves, persistence and cancellation. The two-image regression
  failed before the batch-order repair and passed afterwards.
- GUI replay tests cover marked and legacy carriers, page boundaries, genuine
  attachments, ambiguous origins and unchanged stored messages.
- Scoped agent/LLM, provider/TUI/localization tests and vet passed, as did the
  initial GUI replay regressions.
- The first full run exposed an obsolete checkpoint fixture expecting the
  coordinator to stop without consuming an async report. The updated fixture
  requires that report and exactly one consuming turn, while preserving its
  checkpoint assertions. A subsequent run showed two unrelated compaction
  assertion failures that did not reproduce in 30 focused repetitions, shuffled
  coverage or a complete fresh agent-package run. Diagnostic logging was added;
  failure assertions were not relaxed by this repair. Independently authored
  changes to `window_compact.go`, `window_compact_speculative.go` and the related
  Loop fields appeared concurrently around 00:29–00:33; these were preserved.
  This repair's compaction-test investigation changed diagnostic logging only.
  Final source receipts and full validation include the current combined tree.

The long second validation run was interrupted after its agent package had
already failed and the parent Go process continued spending substantial kernel
CPU time without active test children at the diagnostic snapshot. More than
20 GB RAM was free. Final tests use `-count=1` to bypass Go's test-result-cache
input validation; this is validation tooling, not an application setting.
With test-result caching disabled, the next full run completed in 314.842
seconds. It found lost deadline evidence in checkpoint ref publication, now
covered and repaired as described above. The subsequent full suite passed in
297.049 seconds, followed by full vet, 317 UI tests and all builds. A final
watchdog audit then identified the delegated-wait/terminal-finishing gaps.
After those small final edits, validation repeats agent, GUI and TUI packages,
full vet, all UI tests and all builds against a new complete source receipt.
The preceding full-suite source receipt is preserved as
`full-suite-before-wait-sources.json`, rather than claiming it covered later edits.

Final outcome: affected agent/GUI/TUI tests passed (136.5 s), full vet passed,
all 317 UI tests passed, `go build ./...` passed, both Android ARM64 binaries
cross-compiled, and both Windows executables passed smoke checks. The validated
Windows executables were installed in the repository root with verified SHA-256
hashes; previous versions are in the portable `backup/` directory. The final
source inventory matched at validation, build and replacement. Live delegation
samples after acknowledgment guidance took 14.038, 18.822 and 15.580 seconds,
each with exactly two actual workers, one combined record, three coordinator
tool calls, and zero errors.

Final full-suite/build results are recorded in
`.tmp/termux-fixes-2026-10-15/validation.jsonl`; source hashes in
`validated-sources.json` / `built-sources.json` bind validation to the executable
builds. Logs stay alongside these receipts. Previous Windows executables are
retained in the same directory's `backup/` when replacements are installed.

Limits: no paid Anthropic API call was made, and Android binaries are
cross-compiled here rather than executed on a physical Termux device. Image
transport validation does not promise that a model will identify a person in a
photo. Thinking settings and application data locations are unchanged.
