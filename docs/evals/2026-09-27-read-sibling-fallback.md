# Recovery choices for a missing read — 2026-09-27

## Saved evidence

The fixed GunMayhem snapshot contains a read_many request for internal/masterapi/api.go at sequence 958, returning only not_found. Sequence 964 then separately lists that directory and repeats the missing read in the same batch. The saved listing contains exactly protocol.go and relay.go. The previous recovery helper already inspected the immediate directory, but only reported names with a matching basename fragment; neither real filename matched api.

This demonstrates missing recovery information and a separate directory call. It does not prove that adding the choices will eliminate a whole model turn: the recorded batch also contained other useful work. saved-evidence.json retains the relevant request/error/listing excerpts without reading the live user project.

## Change

When no similar basename exists, the read error now includes all same-extension regular filenames if there are one to three choices and the existing bounded directory scan is complete. Choices are sorted and quoted. Four or more choices, no choices, an incomplete scan, missing directory, cancellation, other error types and short requested basenames keep the existing behavior. Existing lexical matches retain priority.

This applies to read_lines, read_context and read_many through their shared helper. It reuses the existing directory read, reads no alternative file contents, does not recurse, follows no alternative symlinks and never converts the missing file into success. Successful reads incur no new work. No permanent prompt/schema text, model calls, endpoint behavior or storage locations change.

## Verification

- Before the change, the captured-directory fixture failed for all three read tools; after it, each returns protocol.go and relay.go with the original missing-file error identity.
- Six native/thin invocation cases carry the choices to the model and UI while retaining failed status and no alternative file contents.
- Limits cover zero, one, three and four same-extension choices and a directory exceeding the 512-entry scan limit.
- Existing similar-name ranking, sandbox, symlink, cancellation and error-class tests pass.
- Full go test ./... (90-second package timeout), go vet ./..., both builds, CLI --help smoke and git diff --check pass.

The saved case now has the same two filenames available in its failed-read response that previously required list_dir. No live-model saved-turn or latency claim is made. Test and build artifacts are under .tmp/read-sibling-fallback-2026-09-27/.

## Installation

Both portable executables were installed from verified builds. installed.json records hashes and backups; running user processes were not terminated. Restart CLI/GUI to load the change. The broader efficiency goal remains active.
