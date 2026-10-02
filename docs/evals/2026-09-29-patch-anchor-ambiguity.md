# Relaxed patch anchoring must not depend on replacement indentation

## Discovery and reproduced behavior

Review of patch failures in the saved FerrumScope session led to a separate correctness defect in the whitespace fallback. The historical file states are not available for this case, so this report does not attribute an incorrect edit in FerrumScope to the defect.

When an exact old-text match was absent, lineBlockMatch located whole-line anchors after normalizing whitespace. It then tried to re-indent new at each anchor, skipping locations where that conversion was not possible. Only the remaining replacement-compatible locations were counted against expected_count. Two matching old blocks could therefore be treated as a unique match and one of them written, even though replacement formatting does not determine which old block the caller intended.

Before the fix, a regression with two old blocks changed the second one instead of rejecting. Reversing their order changed the first one. A tab/space variant reproduced the same selection error. All fixtures use temporary files under the portable test directory, not the user's project.

## Change

Count all non-overlapping whole-line anchors before preparing any replacement. Reject a count mismatch, then require replacement indentation conversion to succeed at every selected anchor. An incompatible location is never discarded to make the count fit. The anchor list capacity is bounded by the number of possible blocks in the file rather than a potentially huge expected_count.

The exact-byte and line-ending tiers are unchanged. Matching still uses the same whole-line and uniform-indentation rules; no fuzzy text matching was added. The fallback still scans the in-memory line index once, with no extra disk read or provider request and no changes to prompts or schemas.

## Verification

The regression failed before the fix and now verifies rejection with original file bytes preserved. Existing tests cover single valid relaxed changes, expected_count=2 with independently retained indentation, exact-match precedence, CRLF/LF conversion and atomic batches. Native and invoke_tool integration tests verify that both the model and UI receive an error, that the file is unchanged, and that a retry with a unique exact anchor edits only the intended block.

Focused fileops, files and agent patch tests pass. Local reproduction, test results and builds are saved under .tmp/patch-anchor-ambiguity-2026-09-29. This is a correctness fix; no end-to-end speedup is claimed.

Full go test -timeout=90s ./... and go vet ./... pass. Both TUI and Windows GUI builds and their --help smoke checks succeed. Verified local executables were installed with backups kept under the same portable artifact directory.
