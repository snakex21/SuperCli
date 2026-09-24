# Read-only search experiment

See [the evaluation report](../../docs/evals/2026-09-23-search-focus.md) for
setup, exact observations and limitations.

Build with: go build -o .tmp/search-focus/after.exe ./scripts/search-focus

Pass the exact model ID and a fresh absolute output directory inside the app.
Only qwen3.8-27b-uncensored at localhost:1234 and the existing Muse free endpoint
are permitted. The driver creates synthetic files, registers only read/search
capabilities, and prints JSON with events, usage and correctness. It does not
load the user's config, auth, sessions or real repository.

The local model must already be running. Timeout/error cases are recorded rather
than treated as zero-token successes. Latency varies with model state and cache;
compare tool/model counts and correctness as well as time.
