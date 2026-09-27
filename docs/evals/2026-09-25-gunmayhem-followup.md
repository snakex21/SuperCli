# GunMayhem: audit of the 2026-09-25 session

## Scope and evidence

Fixed read-only snapshot of session `de105c910d16e47e`, through message 1571
(2026-09-25 18:06:39 Europe/Warsaw). Raw snapshot and check/build logs remain
locally under `.tmp/session-audit-2026-09-25/`.
The session was still active at capture; this is not its final outcome.

The work below starts with the 10:56 request. The 139.8-minute turn ending
just after midnight belongs to the previous audit and is not counted as new
daytime work. Most recorded work used `space-bunny-free`; the latest request
had switched to `mimo-v2.6-flash-free`. Installed build provenance is not
recorded in this snapshot, so successful behavior cannot be attributed to a
particular binary version.

## Findings

### Continuation works, but delegated work still takes a long time

| Call | Result | Elapsed tool time |
| --- | --- | ---: |
| code worker-1, messages 887–888 | 31 steps; returned an unfinished draft with duplicate declarations | 28m 38s |
| send_message to worker-1, 889–890 | Continued the same worker; 56 cumulative steps; reported focused tests/build passing | 13m 43s |
| review worker-2, 949–950 | 22 steps; concrete DHT/WSS discovery findings | 10m 03s |
| review worker-3, 1371–1375 | Cancelled after 31 steps; no final report | 19m 48s |

The firewall work therefore took about 42 minutes over its initial run and
continuation. The parent did reuse the worker instead of starting from zero.
A notification status of "done" means that a worker run ended: worker-1's first
report explicitly said the implementation was incomplete.

The architecture reviewer also reported attempting `ctx_execute` despite
its read-only role not exposing that tool. This is wasted work worth addressing
in the role/tool contract; it does not justify silently granting command access.

### The main delay is model work and delegation, not local context assembly

Selected closed parent turns, from stored turn metrics:

| End time | Duration | Parent steps | Main observations |
| --- | ---: | ---: | --- |
| 11:22 | 13m 19s | 37 | 162 tool calls, 12 errors |
| 15:54 | 83m 16s | 44 | About 52m 24s inside task/send_message calls; 26m 09s in parent streaming |
| 16:36 | 42m 03s | 84 | About 31m 09s streaming, 6m 22s backend wait, 4m 28s tool execution |
| 17:13 | 20m 04s | 1 | Interrupted delegation; phase breakdown absent |
| 17:41 | 20m 22s | 61 | 128 tool calls, 10 errors |

For the 83-minute turn, context preparation was 0.033 seconds and summarization
31.994 seconds. For the following 42-minute turn, context preparation was
0.189 seconds. These measurements do not support further local prompt-assembly
micro-optimizations as the main remedy for this session.

Different tasks and incomplete run provenance prevent an honest before/after
speedup percentage.

### Confirmed harness defect: a repaired command stayed blocked

The same live Cloudflare test was called at messages 765 and 794. The first
failed with a nil-pointer panic. A successful patch to cloudflare_tunnel.go
appears at 792. The next test failed with a DNS lookup error instead.

At 810–811 SuperCli refused the next identical call because the two failures
were counted across the intervening source edit. The repeat gate tracked only
the tool and arguments for the whole run, even though a test's inputs include
the code being tested.

The fix expires `ctx_execute` failure counts after a successful, non-inert
file mutation. Failed edits, reads, and no-op patches do not expire them.
Other tool-failure counters and repeated-successful-write protection remain
intact. A no-op patch now correctly reports `Inert`.

This change permits a rerun; it does not automatically rerun tools or mark
tests passed. The independent failed-check tracker still requires the actual
successful check. Both CLI and GUI use this common loop, for local and cloud
providers. No provider prompts, tool schemas, or Zen transport were changed.

The scope is observed file-tool mutations within the loop. This does not
attempt to infer arbitrary external changes or shell-side edits.

### Older summary goal is not evidence of losing the latest request

Stored summary rows at 874 and 1446 mention older goals. Automatic compaction
summarizes the old prefix and retains the two latest real user turns and their
tail. The summary is appended to canonical storage but inserted ahead of that
tail in the provider projection. Reading database rows in insertion order can
make this look like the current request was replaced. No compaction fix was
made on that basis.

### Successful unit tests did not establish multiplayer correctness

The 17:41 report claimed serialization/write optimizations, passing tests and
a build, while acknowledging no test on two physical computers. At 18:06 the
user reported multiplayer no longer working. The snapshot does not establish
the root cause or the outcome of the subsequent repair.

The next substantive evaluation should exercise a host and a client with the
actual produced package, including discovery, joining and gameplay. A compact
worker handoff should distinguish checks actually run from those still pending.
The GunMayhem code and running session were not modified during this audit.

## Validation

- Before the fix, the new regression failed for both direct commands and
  PowerShell-wrapped commands: a real file patch succeeded but the repaired
  check was blocked before execution.
- After the fix, the check executes and passes. Reads, failed/no-op edits and
  unrelated failure guards are covered; edits alone do not clear failed checks.
- Full `go test -timeout 90s ./...`: passed.
- `go vet ./...` and `git diff --check`: passed.
- CLI and GUI builds: passed.

These are deterministic regression tests, not a new paid/live-model benchmark.
The fix adds no model calls, context text, repository scans, or retry timers.

Both root executables were updated after a successful CLI `--help` smoke check.
Previous binaries and verified SHA-256 installation records are retained under
`.tmp/session-audit-2026-09-25/`. Already-running processes require a restart
to use the change.
