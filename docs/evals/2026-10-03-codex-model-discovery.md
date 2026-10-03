# Codex model and account integration — 2026-10-03

## Findings and implementation

The former runtime catalog seeded a June model list; imported portable logins could exist without a provider entry. A single named account could incorrectly use the default account manager. Two saved imports also contained UTF-8 BOMs rejected by JSON decoding.

Discovery now uses the native account-authenticated models endpoint (including client_version for the OpenAI host). Only visible returned models are selectable; capabilities and effort levels follow server metadata. Upstream base instructions are neither stored nor inserted into the agent prompt. Successful metadata and bounded failed-attempt timestamps are cached inside the portable data directory, scoped by endpoint and a login fingerprint. Explicit refresh bypasses the five-minute automatic TTL. Saved active/default choices remain unchanged by discovery. Fresh catalogs filter proven absent models from an account pool; unknown/stale catalogs allow normal backend validation.

GUI and TUI auth actions rebuild the transport for subsequent work. The agent receives the raw same model ID through SetAccountProvider, preserving prior history and avoiding a context handoff. A concurrent GUI model selection takes precedence over an auth rebuild. Rejected TUI submissions retain their draft and queued work.

Round robin reserves the next account once per independent completion and retains the last selected account for status. Safe failover stops after content, reasoning, native reasoning, tool calls or output-start markers, including a fragment sharing its delta with an error. Observed fresh exhausted quotas may exclude an account; reset/unknown data does not invent capacity restrictions.

## Validation

- Synthetic native catalogs cover future IDs, metadata, hidden/duplicate/empty catalogs, changing account identity after one 401 refresh, portability, cache TTL and explicit scans.
- GUI integration covers automatic provider creation, stale static entry filtering, no default/active-model changes, and explicit refresh without old model reseeding.
- Auth lifecycle tests cover unchanged history, one transport rebuild, model-switch races and draft recovery. GUI/TUI removal tests cover the selected account and preservation of other accounts.
- The full Go package run passed except for two new test/catalog issues; those were corrected and the two affected packages passed on a targeted rerun. go vet passed for all packages. All 202 GUI tests and 27-language README parity checks passed.
- Read-only live attempts used the three existing portable logins, without model completions. One returned HTTP 403 for catalog/usage; two could not renew credentials. No response body or credentials were written to evaluation logs. This is an external validation limitation: login is required before confirming the accounts' live catalogs. BOM compatibility was reproduced with a synthetic imported file.

See the account-usage evaluation for nullable quota windows, freshness and per-account persistence, and the TUI-history evaluation for allocation measurements. Further optimization auditing is paused at the user's request.
