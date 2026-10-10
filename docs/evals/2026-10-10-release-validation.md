# Release 1.0.5 validation

The native Windows release gate ran the full Go suite with `go test -p 2 -count=1 ./...` and `go vet -p 2 ./...`. Both passed after correcting the checkpoint reference-path failure described below. Compilation/test caches, temporary files and receipts remained under the repository's ignored `.tmp/` directory. No new local-model inference was needed for this gate.

The GUI suite passed 309/309 tests with no failures or skips. README checker tests passed 11/11, and generated documentation passed parity checks for 27 languages. CI and release verification now install the same pinned, development-only DOM dependency using `npm ci --ignore-scripts --no-audit --no-fund`; the shipped application does not require Node.

## Windows checkpoint reference paths

The first full Go run exposed a failure creating a checkpoint active reference when the generated directory and lock paths exceeded Windows' legacy path limits. The fix supplies `-c core.longpaths=true` to each private checkpoint Git command factory, including initialization and retention inventory. It changes neither global Git configuration nor Windows settings.

`TestCheckpointLongPathsWindows` creates a private repository of 195 characters, with generated reference lock paths exceeding 260 characters. Without the fix, the test fails while creating an active reference (`cannot lock ref`, `unable to create directory`). With the fix, it passes capture, completion, Undo, Redo and retention reference inventory. A local false setting remains false afterward, demonstrating the override is command-local. The existing nested-data exclusion and reopened-store regression tests also pass, followed by the full Go gate.

This test covers generated long reference paths. It does not promise initialization of a private repository whose directory itself exceeds 260 characters: native Git initialization has an earlier path-normalization limitation. See [Git for Windows long-path documentation](https://gitforwindows.org/git-cannot-create-a-file-or-directory-with-a-long-path.html).

The release gate builds both executables for Windows amd64, Linux amd64/arm64 and Darwin amd64/arm64. Final package validation checks version/build metadata, executable architecture, packed executable hashes, Unix executable modes, exact manifest sizes and checksums, all five target bundles and public-only portable contents. Native command-line smoke checks cover Windows; Linux/macOS builds are cross-compilation evidence, not interactive runtime tests.

This release does not establish a fix for the historical approximately 116-second compaction delay. That separate investigation did not reproduce the long delay under identical input, and concurrent memory pressure remains a confounding factor. The selected thinking controls and conversation constraints remain unchanged.
