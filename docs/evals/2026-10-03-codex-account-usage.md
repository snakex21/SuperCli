# Codex account usage snapshots

Date: 2026-10-03. This is an account-dashboard correctness change, not a measured inference-speed optimization.

## Problem and resulting behavior

The old dashboard modeled two integer windows and substituted 5h/7d whenever durations were absent. Pool displays averaged those percentages as though all accounts shared quota denominators. A single logout removed every persisted account snapshot.

GUI and TUI now share nullable usage snapshots. Each observation retains server primary/secondary windows, exact nonnegative used percentage, remaining percentage, duration in seconds, reset timestamp, plan, credits, and additional named buckets. Missing/null/invalid values stay unknown. A server-observed 107.25% remains 107.25%; the compatibility scalar and a visual bar may clamp, but the rich observation does not. Reset-after seconds are converted at capture time, so countdowns decrease rather than being restarted on each render. Expiry never invents zero usage.

The official [Codex pricing page](https://learn.chatgpt.com/docs/pricing) states that Pro currently has no five-hour limit; weekly limits may apply. This implementation uses returned duration metadata instead of deriving a quota from a plan name. It cannot infer an unlimited entitlement from a missing window.

## Shared contract and refresh lifecycle

- Cached GET /api/codex/accounts returns account.usage and usage_summary. It only reads portable account metadata and cached files.
- Explicit POST /api/codex/usage accepts a label or all:true. Shared helpers perform serial GETs under a 30-second overall deadline, with a 15-second per-account request cap and the existing single unauthorized-token refresh retry. There is no background usage polling or completion/model invocation.
- CodexUsageSnapshot has captured_at, source, stale, availability (unknown/available/exhausted), plan_type, rate_limits, and credits. Windows preserve nulls. Per-limit and credit captured_at retains old observation age when a normal response only updates general header windows.
- ListCodexAccountUsage[WithOptions] is cached-only; RefreshCodexAccountUsage[WithOptions] performs the manual refresh. RefreshCodexUsageSnapshot provides a single-account convenience path. Options variants honor configured issuer/backend values.
- Account counts deduplicate known aliases of the same server identity. Percentages are never added or averaged in the new displays. Additional model-specific quota exhaustion does not exclude an entire account.

Fresh general-bucket server allowed=true is authoritative, including server-approved credit availability. A fresh denied or exhausted general bucket can be skipped by the router. Unknown, expired, or aged (15 minutes) observations cannot permanently exclude an account. The server still enforces authorization and actual entitlement. This is conservative local scheduling, not a permission bypass.

## Storage, ownership, and account isolation

Snapshots remain inside the resolved portable SuperCli data directory. New per-account filenames use an identity digest, preventing sanitation and Windows case-folding collisions. Old per-account files are read only when their envelope identity matches exactly; anonymous envelopes never match an authenticated identity. Single logout calls ClearCodexAccountRateLimits, leaving other identities intact. The old explicit global reset API remains for compatibility.

Snapshot setters/getters deep-copy pointer fields, window slices, and credits. Header updates may retain earlier account metadata but keep its observation timestamps. Provider state rejects a different live identity, and the unauthorized retry resolves identity again. A manual refresh rechecks login ownership after the GET and rereads current host account metadata, so logout during the request cannot republish authenticated state or its cache. Failed/oversized/malformed requests keep earlier valid observations and return sanitized usage errors rather than raw upstream/auth bodies.

GUI login/logout rebuilds future Codex runs through the root-owned lifecycle helper; passive model discovery failure does not turn a successful login into an authentication failure. Completion request bodies, tool schemas, signed/native history, and the special OpenCode Zen transport are unchanged by this usage work.

## Verification and limits

Durable synthetic tests cover Pro without a primary window; unusual 90-second durations; additional windows; decimal/unknown/negative/over-quota percentages; credits/plan; reset expiry; explicit server entitlement; immutable snapshots; account filename collisions; matching legacy migration; scoped logout; alias deduplication; cached zero-HTTP reads; serial manual refresh; partial failure redaction; response size bounds; unauthorized identity changes; and logout during an in-flight manual GET. The GUI integration follows cached GET, explicit all-account POST, and scoped logout against a local httptest server with configured endpoints.

Initial root focused checks completed llm, account, app, TUI and agent suites successfully. Root fixed an unrelated NewEcho return-arity error in its GUI lifecycle tests and is running the final combined checks after the granted percentage fidelity correction. Final terminal results belong to the parent integration receipt; no test/provider/model process was started by this child while other Go work was running.

No real account usage endpoint was contacted for this change, and no credentials or private auth files were emitted in evidence. Live backend response variants beyond the modeled general/additional usage structure remain unverified. The feature adds bounded snapshot metadata/copies and a small manual-refresh gate; there is no measured RSS, token, prefill, or generation-TPS gain claim.

## Parent integration result

All affected Go packages passed after targeted test corrections, and go vet passed. GUI regressions: 202 passed. README parity: 27 languages passed. Live read-only usage attempts on the saved logins returned upstream HTTP 403 or failed credential renewal; no successful live quota fetch or generation was claimed. GUI/TUI account removal uses the existing scoped logout path, retaining other identities.
