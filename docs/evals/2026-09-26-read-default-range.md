# Bounded default for file-only read_lines — 2026-09-26

## Confirmed problem

The live thin Muse coding probe called read_lines with {"file":"README.md"}. The registry accepted this argument set, but the tool treated the omitted end line as zero and returned an invalid-range error. The native path reproduced the same issue. The failed read consumed a model/tool round; the later overall eight-step evaluation ceiling has additional causes and is not attributed solely to this error.

## Change

- Omitted end line reads up to 300 lines from the selected start, matching the size of a bare read_many entry.
- Start still defaults/clamps to line 1.
- Explicit end lines keep the existing behavior and 500-line cap. Explicit zero or reversed ranges remain errors.
- A default read reaching EOF reports EOF. A longer file returns a continuation line so the bounded result is not presented as the whole file.
- Saturating endpoint calculation avoids overflow at very large start values.
- Existing file resolution, output bounds, cancellation and missing-file handling remain in force.

The tool description was rewritten more briefly to state the default. No system instructions or automatic model calls were added. The shared tool implementation applies to CLI, TUI, GUI and workers on both protocols.

## Validation

Red/green tests reproduce the exact file-only request through native and thin dispatch. Real-file tests cover omitted start/end, explicit start, 300-line bounds, continuation, EOF, explicit invalid ranges, huge start values, missing files and cancellation. Existing range-cap/read-many/read-context tests pass unchanged in behavior.

A live post-fix Muse trace successfully executed {"file":"cache/store.go"} and later completed the coding task with passing tests. An intermediate run was confounded by the evaluation wrapper rejecting a valid Go flag order; it is documented in the live report, not presented as a speedup.

Full go test -timeout=90s ./... and go vet ./... pass. CLI and GUI builds pass.

Artifacts: .tmp/read-default-range-2026-09-26/{before,after,suite,vet,build-cli,build-gui}.json; live traces are in .tmp/worker-coding-2026-09-26/.

CLI --help passed. Both executables were installed with previous-build backups and verified SHA-256 hashes; see installed.json. Running instances require a restart.
