# Reasoning history fixtures

Run from the application/repository root. Go and a headless Chrome/Chromium/Edge
executable are required for the standalone runner; no Node runtime is needed.
All generated files stay under .tmp/history-eval. The probes allow only the
loopback LM Studio endpoint or explicitly selected free OpenCode Zen models.
They do not load credentials, user sessions, project files or execute shell tools.

PowerShell:

    New-Item -ItemType Directory -Force .tmp/history-eval
    go build -o .tmp/history-eval/probe.exe ./scripts/reasoning-history/probe
    go run ./scripts/reasoning-history/run experiment

The runner retains the existing Go probe's JSON input/output and HTTP transport,
including endpoint restrictions, session handling, streaming and request parameters.
Build `probe.exe` as above before running an experiment (the same filename is used
on every platform). No credentials or application configuration are loaded.

Positional arguments are unchanged; there are no named flags:

    go run ./scripts/reasoning-history/run [output-directory [model1,model2 [fixture-id]]]
    go run ./scripts/reasoning-history/run experiment qwen3.8-27b-uncensored escaped-fields

The default directory is `experiment`. Empty filters select all four models or
both fixtures; unknown filters produce an empty `results.json`. Model selection
keeps catalog order. Fixture IDs are `escaped-fields` and `cache-expiry`.
Relative output directories resolve under `.tmp/history-eval`; absolute paths
are accepted only within that same directory, including the directory itself.
Each A/B pair shares the same completed planning history; only historical native
reasoning is removed in DROP. Native reasoning inside the current tool turn is
kept in both arms. Server availability, random generation and order affect timing.
JSON-line `request-complete` / `arm-complete` events, numbered request archives,
metric fields and per-arm `results.json` checkpoints retain their existing shape.
A provider error is recorded in stats; a probe/process or infrastructure failure
exits nonzero. Exit zero alone does not mean fixture verification succeeded.

Verification executes the actual returned JavaScript, with the original 9 parser
and 7 cache assertions, in a fresh Blob Web Worker on an otherwise empty headless
page. A main-page watchdog terminates that Worker after 500 ms, including for an
infinite loop, and returns `passed` / `total` / `success` / `got` or `error`.
The owned browser uses a fresh temporary profile, not an existing user session;
the fixture page disallows network access and dynamic code generation. This is
fixture isolation and a timeout, not a security sandbox. Set
`SUPERCLI_TEST_BROWSER` to select a Chrome/Chromium/Edge executable if automatic
discovery does not find one.

Local checks use only example JavaScript and a loopback HTTP mock, never models:

    go test ./scripts/reasoning-history/run
    go build -o .tmp/history-eval/run.exe ./scripts/reasoning-history/run

The integrated probe uses the actual agent Loop and SQLite reconstruction:

    go build -o .tmp/history-eval/live-loop.exe ./scripts/reasoning-history/live-loop
    ./.tmp/history-eval/live-loop.exe qwen3.8-27b-uncensored "$PWD/.tmp/history-eval/integrated-local"
    ./.tmp/history-eval/live-loop.exe muse-spark-1.3-contributor-free "$PWD/.tmp/history-eval/integrated-cloud"

The integrated probe captures requests/responses containing only its synthetic
fixture. Inspect both errors and generated output; process exit zero is not a
model-quality pass. See docs/evals/2026-09-23-native-reasoning.md for measured
results, the failed initial local smoke, limitations and implementation policy.
