# Show the actual patch mismatch — 2026-09-26

## Evidence

The fixed GunMayhem session snapshot contains a failed README edit at sequence 1319. The first 795 of the requested 824 bytes matched on line 113. The error identified that line but displayed only its first 120 bytes, hiding the actual disagreement near the end:

- Requested: See `MULTIPLAYER.md` for details.
- Actual: See [MULTIPLAYER.md](MULTIPLAYER.md) for details.

At sequence 1321 the model searched README for the paragraph again; the search returned the complete 840-byte line. Sequence 1325 successfully retried the edit with the current text. That later search was batched with other work, so this trace does not prove an entire provider turn would have been saved.

The exact old text and the line recovered from the saved search result were copied into .tmp/patch-divergence-2026-09-26/saved-replay.json. Replaying the failure against the previous code reproduced the hidden mismatch; the updated code includes the actual Markdown link in the error. User project files were not opened or modified, and no provider calls were made for this change.

## Fix

The existing 120-byte source excerpt now follows the divergence offset, reserving most of its budget for the unmatched text. Long lines are explicitly clipped with ellipses; short lines retain their full text.

Two associated defects were reproduced and fixed:

1. When the first differing byte was a newline, the error claimed line N but printed line N+1. The excerpt now agrees with the reported line number.
2. Truncation could cut a UTF-8 character and show escaped incomplete bytes. Excerpt boundaries now preserve complete characters. EOF after a final newline is explicitly identified.

This changes failure diagnostics only. Exact matching, ambiguity checks, stale-hash protection and write atomicity remain unchanged. In particular, the intentional refusal to guess between tabs and spaces remains in place. Successful patches incur no new work or result text. Failure formatting uses the buffer and divergence offset already available in memory; no additional file read, model call, schema text or permanent prompt is added.

## Verification

- Red/green replay of the saved sequence-1319 failure.
- Long-line mismatch, newline boundary, Unicode mismatch and truncation, EOF with and without newline; failed calls leave bytes unchanged.
- Both native patch_file and invoke_tool paths preserve the useful error for the model and UI event.
- Existing patch diagnostics and all fileops tests pass.
- Full go test ./... and go vet ./... pass; CLI and GUI builds pass.

Artifacts: .tmp/patch-divergence-2026-09-26/{saved-replay,red,green,green-final,protocols,suite,vet,build-cli,build-gui}.json. The first green run exposed a Unicode alignment edge case, corrected in green-final.json. No latency or live model-turn reduction is claimed: the proven improvement is that an already-required error response contains the relevant current text instead of an unrelated line prefix.

## Installation

CLI --help smoke passed. Both portable executables were installed and verified against the built SHA-256 hashes; installed.json contains hashes and backup paths. Existing processes were not terminated. Restarting CLI/GUI loads the update. The broader efficiency goal remains active.
