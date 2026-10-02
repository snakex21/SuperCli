# Grouped reads: recover a deterministic range list

## Recorded failures

The saved FerrumScope session ce6986b1affaf49d contains two read_many calls (assistant seq 1134 and 1702) rejected with expected string, got object. Both supplied reads as an object containing only item, whose value was a list of file:from-to strings. All paths and ranges were already explicit; the failure was the representation.

The advertised schema remains reads:string, using the compact file:from-to | file:from-to syntax. The original calls have not been rewritten in the session database.

## Fix and boundaries

After schema rejection, the existing local argument-repair hook accepts a nonempty list of range strings, either directly or inside the singleton item wrapper, and joins them in the supplied order with the existing delimiter. The resulting arguments pass ordinary schema validation and the unchanged read_many handler. Valid calls bypass the hook. No tool description, schema, system instruction or provider-specific path was added.

The hook rejects duplicate object keys, extra root/wrapper fields, null or blank elements, mixed/structured entries, trailing JSON, elements containing the delimiter and batches above the existing 12-request limit. It does not infer paths, line ranges or missing values. Existing sandbox resolution, per-range limits, grouped file scanning, cancellation and output bounds remain in force. Output-driven compaction facts and status handling do not depend on the repaired argument representation.

## Verification

A regression through the real registry failed for both direct string arrays and the item wrapper before the change. It now returns byte-identical output to the canonical request, including out-of-order ranges and repeated ranges from one file. Negative cases verify no handler dispatch. Agent integration tests verify native calls and invoke_tool both receive one successful result in one dispatch, with the same result delivered to the UI.

Focused tests pass. This demonstrates recovery of the recorded argument-format failures; it is not a live-model timing measurement or a guarantee about later model decisions. Local before/after records and protocol checks are saved under .tmp/read-many-format-2026-09-29.

Full go test -timeout=90s ./... and go vet ./... pass. Both TUI and Windows GUI builds and their --help smoke checks succeed. The local executables were replaced after verification, with previous files backed up in the same portable artifact directory.
