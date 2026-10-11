# Startup observation and performance follow-up audit

## Changes

- `process_session` start now awaits its completion event for up to 500 ms
  by default (previously 250 ms). Completion returns earlier. Explicit
  `yield_ms: 0` still means an immediate launch; a longer supported window
  can be requested. This is one bounded wait, not status/log/port polling.
  A process that exits later cannot be diagnosed at an earlier start return.
- Cancelling the startup observation leaves an already started process and
  its unread output available through its id. Pre-cancelled requests do not
  launch. The independently configured process lifetime is unchanged.
- Native exits publish their code and timestamp before indicating exit.
  Snapshots with pending output capture use `exited_draining`, retain the
  known exit code and expose `output_pending`. A known nonzero code reaches
  the model immediately; `wait` retrieves remaining output without relaunching.
- `ready_at` remains the last historical readiness observation. After exit
  or stop, `ready` is false and the status summary explicitly labels the
  timestamp as past evidence. Structured compaction also labels a retained
  past readiness timestamp separately from current readiness.
- Other-project notes are explicitly labelled as abbreviated navigation
  context, not current-project instructions. Ordinary prose previews end at
  word boundaries and remain UTF-8/byte bounded. If a note cannot fit, the
  project name can still fit. Stored full cards and journals are untouched.
- Automatic implementation/verification guidance now lives in the existing
  request-only context tail. It applies during a requested editing turn and
  its tool steps, but is absent from canonical history and later read-only
  turns. This prevents an identical guidance block accumulating for each
  coding turn, including worker continuations. The shared context estimator
  counts the existing tail, so it also counts this guidance.
- Shared search/recursive-list policy now also excludes `bin`,
  `supercli-data-preview` and literal `%SystemDrive%` descendants. Existing
  `.tmp`, `supercli-data`, `dist` and dependency exclusions remain. Selecting
  one of those directories explicitly still allows inspecting it.
- The subprocess environment allowlist retains `GOTMPDIR` alongside
  `GOCACHE` and `GOMODCACHE`. It does not relocate any application data.
- Whole-project regression exposed a discovery-description issue: the command
  tool no longer mentioned builds/tests, so command queries also activated
  unrelated document/list tools. The description now names those actual
  capabilities, restoring the existing generic relevance/coverage ranking;
  no site/model-specific routing rule was added.
- Stats row visitation explicitly checks caller cancellation. Buffered SQLite
  rows could previously continue reaching callbacks after cancellation despite
  the original `QueryContext` being cancellable. No accounting record is
  changed or deleted by this read-path fix.

These changes neither select a particular model nor change the user's
reasoning/thinking setting. They add no classifier, summarizer or other
model request. The extra default startup observation is bounded; ordinary
one-shot commands continue to use `ctx_execute`.

## Review of the supplied checklist

Local/private model endpoints already default to the thin tool protocol,
stable tool definitions and catalog hoisting in
`internal/system/execution/profile.go`. Tool activation exposes schemas
through the existing dispatch/result mechanism without growing `tools`.
Byte-for-byte activation and prefix tests cover this. Live memory and
freshness are already in the request tail rather than the leading system
block. Explicit user overrides remain respected.

The local model endpoint recorded in application configuration,
`http://localhost:1234`, refused a single read-only model-list request during
this audit. No server/model was started, reconfigured, unloaded or otherwise
changed. A live prompt/cache latency measurement was therefore unavailable;
request-stability tests are not a substitute for measured `prompt_n`/cache
telemetry. The quoted 10–13-token/0.2-second target is not claimed here.

The existing checkpoint implementation has bounded snapshots, exclusions
for application data and safe retention. Its size limit is 256 MiB per
selected file; it does not silently claim that a hash can restore omitted
content. Blanket removal of binary/archive files from explicit mutations
would weaken undo and is not applied. `badcheckpoints` and previous build
artifacts are retained; removing recoverable backups is separate from this
code change.

A one-time inventory measured logical file lengths (not allocated disk
blocks): `.tmp` 32,057,037,957 bytes / 125,611 files,
`supercli-data/badcheckpoints` 1,238,827,500 bytes / 11,010 files,
literal `%SystemDrive%` 957,024 bytes / 6 files. These generated/data trees
remain excluded rather than being scanned for routine code search. No space
reclamation is claimed. Temporary test/build data stays under the application
folder; moving it to `%LOCALAPPDATA%` contradicts the user's portable-data
requirement and is not used.

`SystemDrive` is already preserved by the case-insensitive Windows environment
allowlist. The original producer of the old literal-name directory has not
been established. Its protective `.gitignore` entry remains; no historical
system/cache files were removed.

The two root-level user notes about occasional WebView tabs and checkpoint
size are retained. They have not been converted into invented reproduction
steps or silently deleted. Process-window click/type support is a separate
feature proposal; this audit does not implement it. Existing uncommitted
changes are preserved and are not automatically included in a commit/release.

## Validation

Focused regressions cover immediate and delayed failures, native Windows
PowerShell, start cancellation, explicit zero-yield, readiness after stop,
pending output capture, repeated guidance/worker continuations, Unicode
previews, tight memory budgets, unchanged stored records, search exclusions
with explicit-root access, and retained portable Go environment variables.
Full test/vet/build outcomes and source/binary verification receipts are
recorded under `.tmp/process-start-followup-2026-10-11/` after completion.

Final `go test -p 1 -count=1 ./...` passed in 76 tested packages, followed by
successful `go vet -p 1 ./...`, `go build -p 1 ./...` and `git diff --check`.
The first whole-project run exposed the multimodal test's previous placement
expectation, buffered-row cancellation and command discovery; these were
repaired, checked three times, then the whole-project run was repeated.

Native PowerShell's code-3 failure and Polish stderr were observed directly
by `start` after 119 ms on this host. A deterministic 300-ms delayed-failure
fixture covers the widened default observation window. Slow native startup
can still outlive a bounded window and is not falsely marked failed.

The unchanged command-discovery regression measured 7,619 response bytes /
2,408 estimated tool-result tokens before the description repair versus
1,623 bytes / 523 tokens after it, returning only `ctx_execute`. This is a
fixture's discovery-output reduction, not a whole-model latency percentage.
Explicit multi-intent queries and explicitly broad limits remain covered.

An exact-source Go overlay (verified against the pre-change SHA-256) measured
the real repository's search traversal before/after the new exclusions;
live source was not reverted. Enumerated files fell from 2,892 to 2,860,
single walk times were 14.843 / 12.677 ms. Three no-hit `search_code` samples
were 450.124, 106.544, 119.872 ms before and 111.201, 105.011, 116.247 ms
after, with identical results. The cold first sample must not be read as a
4x code speedup; warm samples are approximately 0.1 s in both arms. Host/cache
noise and three samples do not establish a meaningful warm latency gain.
