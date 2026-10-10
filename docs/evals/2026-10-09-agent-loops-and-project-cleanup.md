# Agent loops and portable checkpoint cleanup — 2026-10-09

## Changes

The agent compares completed observations, including their actual results, rather than treating identical arguments as proof of a loop. It gives a recovery hint after repeated observations and aborts after eight consecutive rounds containing only previously seen successful evidence. A changed result, mutation, failure or new instruction breaks this streak. An independent last-resort budget covers 50 consecutive identical failures. Stopping reports the reason and preserves partial conversation history; it does not report an unverified success.

Trusted read-only web tools can reuse a successful result within the current run for at most two minutes. The cache holds at most 16 entries, 1 MiB total and 128 KiB per entry. It validates normalized arguments on every call, coalesces simultaneous duplicates through completion notifications, and is cleared on mutations, new instructions and run boundaries. `refresh=true` requests a new observation and prevents an older in-flight read from replacing the fresh result. Errors, image results, retained output and failed verification are excluded. No additional model call or site-specific branch is introduced.

The project UI offers checkpoint size preview and explicit cleanup, plus removal choices that can remember whether checkpoint history should be cleared with a project. GUI and CLI share portable `project-cleanup.json`. Missing preferences retain history for compatibility. The current installation is configured to clear history on explicit project removal; users can override once or persistently keep it. A missing project directory or disconnected drive never triggers cleanup.

Cleanup is restricted to the selected workspace's exact checkpoint hash and a recognized archive. A portable store gate, live operation leases, active Git roots, journal checks and filesystem identity checks protect the operation. It reads file metadata rather than multi-gigabyte blobs. Conversation rows, memory and workspace files remain intact. Cancellation returns only confirmed removed bytes and leaves accounting dirty for a subsequent census. A cached Manager recreates checkpoint history on its next capture and can Undo subsequent edits.

## Storage audit

The read-only preview for an active registered workspace returned **8,578,348,729 bytes**, **2,706 files**, **2 stores** (about 7.99 GiB). No production checkpoint was deleted during development or installation.

The earlier total-folder audit found about 9.71 GiB under portable application data. Large legacy Git-index roots and archived bundles account for much of the retained storage. The retention baseline is not a strict cap on protected Undo history. New captures already stream hashing and compression with bounded buffers; these multi-gigabyte files are not loaded wholesale into RAM.

## Application integration

The existing generic MCP bridge lazily discovers a package's tools through portable manifests and one stable list/search/call interface. Shared process execution and completion waits also exist. Applications exposing an API or scripting interface can use that bridge with a small package describing their capabilities, instead of adding a separate CLI surface and permanently exposing every application's schema. No DaVinci-specific integration or universal Undo for external applications was added in this change.

## Verification

- Full agent package passed (23.576 s).
- All 249 JavaScript UI tests passed.
- Focused command, GUI HTTP, checkpoint and portable preference tests passed, including cancelled deletion, active owners, path aliases, unrelated data preservation, explicit policy overrides and same-Manager capture/Undo after cleanup.
- Full checkpoint (206.878 s), TUI, application commands, tool registry, web tools and session/memory packages passed. Full GUI (59.745 s), prompt, sandbox and command-runner packages passed. Vet for the changed packages and both executable entrypoints passed.

The live conversation uses normal GUI session reconstruction, automatic delegation, repository preflight and maximum thinking. It supplies no media URL or fake tool result. All requested output directories are explicitly under `.tmp/gif-followup-live`, including the initial Pobrane directory, so this run does not write into the user's real Downloads directory. This differs from the prior live fixture's initial Downloads request and must not be treated as a controlled timing benchmark.

## Completed live conversation

The single run at `.tmp/gif-followup-live/run-1064445823/receipt.json` passed all three turns, producing four valid GIFs; the final pair has distinct SHA-256 hashes. Helpers and auxiliary model calls were zero throughout.

| Turn | Wall time | Model calls | Tool calls | Tool failures | Tools after last save | Model calls after last save |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Initial GIF | 55.821 s | 4 | 3 | 0 | 0 | 1 |
| Follow-up in a new folder | 59.089 s | 8 | 7 | 1 | 1 | 2 |
| Two distinct cat GIFs | 72.309 s | 6 | 8 | 0 | 0 | 1 |

There were no identical successful web reads in this run, so result reuse was zero. Its deterministic regression separately verifies that three identical lookups execute the underlying function once, while both requested saves still execute. The model still made redundant decisions: several different lookups, and one attempted repeat download after success. The existing-file guard rejected that attempt without another HTTP request or overwriting the saved GIF. This run verifies completion and safe recovery, not a minimal number of actions or a general speedup.

Tool execution accounted for 1.431 / 4.241 / 4.637 seconds. Provider wait and streaming accounted for roughly 92–97% of turn time, so maximum-thinking inference remains the dominant cost.

The original receipt's `main_requests_after_last_save` fields incorrectly read zero because this fixture omitted the `provider_entry` marker. The table derives these counts from the durable message transcript; the fixture now emits the marker for future runs. The original evidence is preserved unchanged.

## Installation

Both executables were rebuilt and atomically replaced. Their previous versions remain under `.tmp/anti-loop-fix/backups/d6e62cc1-0934-461d-9825-5d50d8b38267`. Installation, policy, source and help checks are recorded in `.tmp/anti-loop-fix/{installed,policy-installed,source-proof,help-smoke}.json`.

- CLI SHA-256: `E402C7F06F6CA0E14EB97AD348B6A0143E7A4F1FCCC838BECA8AC0E5669928EC`
- GUI SHA-256: `DB6EA00074A9E681EDBC8CFFA303F0E4FDECE2C25E50D560A9377A3EE6D72E2B`

`supercli-data/project-cleanup.json` enables checkpoint cleanup on explicit project removal. The user can choose retention once or disable this preference in the removal dialog or `/projects cleanup-policy keep`. Existing processes keep their loaded executable and need a restart for the code changes.
