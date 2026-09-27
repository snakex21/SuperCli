# Preserve read_many outcomes during pruning — 2026-09-27

## Confirmed loss

The current read_many renderer already preserves every file header/error in a balanced preview and retains larger captured bodies. That path did not need another preview change.

The later pruning step, however, replaced successful, partially failed and entirely failed batches with the same neutral read_many marker plus a retrieval reference. The tool-call arguments remain, but they do not reveal which outcome occurred. Direct and invoke_tool calls both lose this distinction. The frozen GunMayhem history contains real mixed/all-failed read batches; it does not contain the provider-facing pruned views, so it cannot establish that this loss caused a particular historical reread.

A filesystem-backed reproduction using the current registry, result store and pruning pass confirms the loss in all six direct/envelope × successful/partial/all-failed cases. The original output contains the generated counts; the pruned provider view does not.

## Change

For read_many only, retain the generated summary counts as ok=N, failed=N in the existing marker. Parse only the known first-section framing and complete terminal summary, allowing the output store's validated retention footer. Canonical non-negative integer counts must fit the tool's public 12-item batch bound; incomplete, inconsistent or malformed metadata stays unknown.

The existing paired-call resolver also recognizes read_many behind invoke_tool for this interpretation. Persisted names, call IDs, arguments and provider pairing remain unchanged. No execution, permission, retry, verification or pruning-threshold policy changes.

This adds 15–16 bytes to a pruned batch marker, not to persistent instructions or ordinary read results. Large results still shrink to roughly 110 bytes (135 with the inline archive's offset/limit in the fixture), and their full detail remains recoverable. There are no new model calls or source reads.

## Validation

- Six red-before/green-after integration cases cover complete success, partial failure and all-failed reads through direct/native and thin/invoke_tool routes.
- Both previously stored results and newly archived inline errors retain their reference; read_output searches the original summary without re-executing the source read.
- The next provider-facing view contains the same counts and unchanged call/result pairing.
- Fourteen malformed/source-text cases remain unclassified. Legacy generated all-failed summaries without an error prefix remain readable; similar words from read_lines are not treated as read_many metadata.
- Existing pruning/retrieval/late-dispatch tests, full go test ./... and go vet ./... pass.
- CLI and Windows GUI builds and CLI --help smoke pass. Both executables are installed beside the portable application data with backups and verified hashes.

This verifies preservation of outcome evidence under pruning. It does not measure a live-model turn or latency reduction.

Artifacts: .tmp/read-many-prune-outcome-2026-09-27, including red.json, green.json, focused.json, full validation logs, installed.json and source backups.
