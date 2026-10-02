# Patch arguments: omitted replacement could delete text

## Discovery

Review of the patch_file paths after the FerrumScope audit found a separate correctness defect. The saved FerrumScope snapshot contains no short-form patch calls, so this report does not attribute a deletion in that project to this bug.

The short-form schema permits old/new as optional root fields, with path required. The handler previously decoded new into a string, making an absent or null value indistinguishable from an explicit empty replacement. A registry-dispatched call with path and old, but no new, consequently deleted the anchor. The direct batch handler also treated absent/null new as deletion, although registry validation already rejected malformed batch items. Empty short-form old/new supplied alongside changes could be silently ignored.

## Fix

Decode old/new as nullable pointers in the existing single JSON pass and require both values before forming a replacement. Explicit new=empty-string remains intentional deletion. Reject supplied short-form fields alongside a nonempty changes list. Validate all change pairs before invoking the atomic patch engine; malformed input writes nothing. Tool descriptions and schemas are unchanged, with no extra model request or repeated instruction.

## Verification

A regression reproduced deletion through the real registry and direct handler before the fix. It also reproduced partial interpretation of mixed forms and malformed batch replacement handling. After the change, all cases reject with original bytes preserved. A registry-level test verifies explicit deletion still succeeds. Existing tests retain coverage of shorthand replacement, multiple occurrences, 200-change batching, path argument repair and sandbox boundaries.

Focused files, fileops and core tests pass. Full go test -timeout=90s ./... and go vet ./... pass. Both TUI and Windows GUI builds and their --help smoke checks succeed. Local before/after results and final builds are saved under .tmp/patch-presence-2026-09-29.
