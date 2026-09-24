# Reasoning history fixtures

Run from the application/repository root. Go and Node.js are required.
All generated files stay under .tmp/history-eval. The probes allow only the
loopback LM Studio endpoint or explicitly selected free OpenCode Zen models.
They do not load credentials, user sessions, project files or execute shell tools.

PowerShell:

    New-Item -ItemType Directory -Force .tmp/history-eval
    go build -o .tmp/history-eval/probe.exe ./scripts/reasoning-history/probe
    node scripts/reasoning-history/run.cjs experiment

Optional model/fixture filters: inspect the runner's argument handling at its end.
The output directory name must be relative and remain below .tmp/history-eval.
Each A/B pair shares the same completed planning history; only historical native
reasoning is removed in DROP. Native reasoning inside the current tool turn is
kept in both arms. Server availability, random generation and order affect timing.

The integrated probe uses the actual agent Loop and SQLite reconstruction:

    go build -o .tmp/history-eval/live-loop.exe ./scripts/reasoning-history/live-loop
    ./.tmp/history-eval/live-loop.exe qwen3.8-27b-uncensored "$PWD/.tmp/history-eval/integrated-local"
    ./.tmp/history-eval/live-loop.exe muse-spark-1.3-contributor-free "$PWD/.tmp/history-eval/integrated-cloud"

The integrated probe captures requests/responses containing only its synthetic
fixture. Inspect both errors and generated output; process exit zero is not a
model-quality pass. See docs/evals/2026-09-23-native-reasoning.md for measured
results, the failed initial local smoke, limitations and implementation policy.
