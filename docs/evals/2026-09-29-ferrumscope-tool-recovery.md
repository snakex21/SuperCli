# FerrumScope tool recovery audit — 2026-09-29

## Evidence

Read-only analysis of saved session ce6986b1affaf49d (space-bunny-free, Zen): about seven hours between the first and last recorded activity, 2,468 messages and 1,346 tool calls. The snapshot contains 247 explicit tool failures, including ordinary compiler/test failures; this is not a harness defect rate. Of 108 patch_file failures, 64 failed argument validation. In 47 of those calls, an unknown field error concealed a simultaneously missing old field in the same change object.

## Verified causes and changes

1. At message sequences 43 and 1755, ctx_execute failed to locate project-relative UPX/Zig executables although workdir pointed at the project. Binary lookup used the SuperCli process directory before setting the child working directory. Explicit relative executable paths now resolve against validated workdir. Bare command names still use PATH and bundled rg discovery; execution remains direct, without a shell. Missing explicit paths receive a path-specific diagnostic.
2. Invalid patches containing new_ignore and no old were rejected with only the unknown-key diagnostic. Validation now reports missing required fields from the same object in that response, while retaining the valid-key list and typo guidance. It never invents an edit anchor or drops malformed changes. The complete patch is rejected before any file write. The extra diagnostic is generated only for invalid calls.

## Context check

Some compaction summaries describe an older project phase. The stored projection through sequence 2387 nevertheless retains both the Stage 3B request and the latest toolchain request verbatim. This audit does not establish loss of the current task, so it makes no compaction change. The session contains no delegation calls.

## Verification

- Before the fix, targeted regressions reproduced both defects.
- A real child-process test covers relative, dot-relative, forward-slash, parent-relative and absolute executable paths, a working directory containing spaces, literal arguments and a nonzero exit code.
- A registry-level patch test verifies both diagnostics, no partial write on invalid input and success after correcting the call.
- Focused ctxexec, files, core and workflow package tests pass.
- Full go test -timeout=90s ./... and go vet ./... pass. TUI and Windows GUI builds succeed.
- Detailed local results are stored under .tmp/ferrumscope-audit-2026-09-29.

These are deterministic harness corrections. No live-model latency improvement or elimination of the 47 observed invalid calls is claimed. Compiler, driver and hardware-assumption errors still require correct project work by the agent.
