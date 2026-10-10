# Persisted usage details and GUI layout — 2026-10-10

The statistics sidebar retains its compact layout. Clicking the session token
total opens a model and purpose breakdown. The popup reads the portable billing
journal on demand; it starts no inference, transcript reconstruction, exchange
lookup, retry timer or background refresh loop.

Input and output are the provider's recorded counters. Cached input is part of
input; reasoning is part of output. Neither is added again to the total.
Unavailable counters remain unknown, and partial reporting shows coverage.
Tool context is an explicitly labelled estimate of tool-role input across
requests, not an exact charge for executing tools.

The journal now retains the available request-shape estimate after deleting a
conversation. Existing surviving records are enriched once, using a durable
batch cursor. Normal and full portable backups retain these SQLite columns.

Opening a saved conversation restores its latest persisted response telemetry.
Its model identity comes from a bounded main-call query, not the active selector
or a later helper. If only a main-call record exists, the GUI labels that smaller
scope. Historical records without a stream-generation clock do not invent a
generation speed.

Some older conversations have only session totals. A one-time migration captures
unambiguous sessions with no detailed usage or journal rows. These remain
unattributed aggregates, not model rows or invented API calls. Partial gaps are
reported separately without increasing the measured total. Synthetic aggregates
are excluded from today's usage and cannot use a single date's exchange rate
for a potentially multi-day conversation. Prices and already saved exchange
snapshots remain immutable.

The currency select previously inherited a global width of 100%, squeezing its
description into a narrow column. A currency-specific sizing and wrapping rule
keeps the description readable and preserves the user's font and scale choices.

The reported NBP HTTP 403 was reproduced against the same historical URL:
`Go-http-client/1.1` returned 403 and a SuperCli identifier returned 200.
Requests now identify the actual application as `SuperCli/1.0`; HTTPS,
certificate checks, redirect restrictions and response validation remain.
A live test through the real Go cache fetched the 2026-10-09 table successfully
in 0.14 seconds. The documented [NBP API](https://api.nbp.pl/) remains the sole
exchange source.

Verification receipts, isolated example data and screenshots live under
`.tmp/preflight-stats/` inside the application folder. The example data are
explicitly synthetic and contain no real user conversation or billing amount.

## Performance and thinking settings

Cold native-Git preflight now reads the branch from the status response and
runs the independent log lookup concurrently. A repository requires two Git
processes instead of three. The non-repository error path still requires two;
bare repositories, worktrees and Git environment overrides remain supported.
The resulting briefing was byte-identical in the measured comparison.

Counterbalanced local measurements:

| Fixture | Previous median | Current median | Change |
| --- | ---: | ---: | ---: |
| Clean repository | 82.594 ms | 46.729 ms | -43.4% |
| Six changed paths | 81.893 ms | 46.253 ms | -43.5% |
| Two hundred changed paths | 83.401 ms | 47.551 ms | -43.0% |
| Outside a repository | 42.974 ms | 43.237 ms | +0.6% |

The repeated-summary input excludes only the recognized fixed resume envelope
around the previous compact summary. The summary body, recorded conversation,
ordinary messages, tool messages, output envelope, prompt, generation budget
and reasoning configuration are unchanged. In one completed-summary fixture,
the helper transcript decreased from 1924 to 1774 bytes, or 577 to 536 estimated
tokens. The body hash remained identical. This does not establish a reduction
in Qwen's generation time.

One real request through the application runtime completed with HTTP 200 in
22.982 seconds (first token 1.202 seconds), without retries or tool calls.
It reported 736 input, 797 output and 528 reasoning tokens. Configuration
provenance matters: the global setting was `max`, while the existing workspace
override resolved to `xhigh`; the factory and request preserved `xhigh`.
No user setting was edited. The harness now checks the expected resolved effort
before discovery or inference. This is a runtime smoke check, not an A/B speed
comparison or evidence that the backend accepted `max`.

Detailed benchmark receipts and immutable source hashes are in
`.tmp/preflight-compact-efficiency-fix/`. Older evaluations are retained as
historical observations and are not used to claim a generation speedup.

The remote changes were inspected after fetching GitHub. The local checkout
and `origin/main` both resolved to `10ad047`; the recent cancellation, indexed
lookup, generation timing and tool-argument fixes were already present.

## Final verification and installed binaries

All 14 affected Go targets passed their tests; `go vet` passed for the same
targets. The GUI suite passed all 295 tests. This includes a regression for
the translation scanner: dynamic `i18nEl` arguments cannot accidentally consume
a later DOM attribute such as `scope="col"`. No translation checks were disabled.

The real GUI was also checked with an isolated portable SQLite fixture, Polish
labels, Georgia and 140% interface scale. Opening the saved conversation after
reloading restored 216100 total tokens and its persisted last-turn counters
(27000 input, 3000 output, 2000 reasoning, six tools). The model/purpose popup,
historical-cost popup and compact currency selector were checked visually.
The browser console contained no errors. Screenshots and the verification
receipt are in `.tmp/preflight-stats/visual-final/`; all their amounts are
explicitly example data. Only the isolated GUI process and its test browser
tab were closed.

Both executables were built from a source manifest verified before and after
the build, installed atomically with checked backups, and passed `--help`
after installation:

| Binary | SHA256 |
| --- | --- |
| `supercli.exe` | `6DFBB940D157B4A255C2E3F508B424D8CA4AD63E42227A02EF24DB7AE83D32FD` |
| `supercli-web.exe` | `24E6F25DF845CEDBC44916E5B77B095F1549AD26F39218896E952C8BB1135188` |

The previous executables are retained in
`.tmp/preflight-stats/backups/51ae26d4-5d88-4731-b0c9-3010f6e7c310/`.
No running user GUI was terminated. Reopening the application loads the new
version and performs the tested portable database migrations.
