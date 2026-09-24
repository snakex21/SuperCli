# Search outcomes must not invent tool failures

2026-09-24. Shared verifier used by CLI/TUI and GUI.

Investigation of repeated reads found no rule requiring a second file read
after a successful search with context. Models still sometimes choose that
extra step. A separate deterministic bug was found in search verification.

Previously a successful search whose text began with "no results" or "not found"
was rewritten into an error. This also rejected an actual search_code hit from
a file named "not found.go" in location-only mode. The error set the agent's
concreteFailure state and could block goal completion until another successful
check, despite no backend failure.

The verifier now accepts a reported no-match outcome and trusts Result.Err for
backend failures. Completely blank responses still fail validation, and custom
per-tool verification is unchanged. No prompt, schema, model call, cache or
OpenCode Zen transport change is involved.

Before/after regression tests use the real agent dispatch/verification/completion
path and an actual temporary search_code fixture. Before the change, the two
no-match strings and the real filename case failed; after it, they pass. A
backend-error control still blocks completion. These are correctness tests,
not a measured claim of fewer model turns or lower wall time in user sessions.

Validation: full go test -timeout=90s ./... and go vet ./... passed.
Build, startup and installation results are saved alongside the test evidence
in .tmp/search-result-verification/. All state stays inside the application.
