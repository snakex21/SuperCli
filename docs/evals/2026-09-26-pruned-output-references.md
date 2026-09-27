# Retained output references survive context pruning — 2026-09-26

## Evidence and reproduction

The fixed GunMayhem snapshot contains 89 large-output previews. It also contains repeated reads of the three firewall files at sequences 897, 906 and 931. These repeats guided the context audit; the snapshot does not establish that pruning caused those specific repeats.

A current-code regression confirmed a separate retrieval failure: maybePruneToolResults replaced a large-result preview with a generic marker, removing its output handle. The full result remained in the existing bounded output store, but the model no longer had a direct reference to retrieve it. A worker without a conversation writer could not recover that reference from its own saved transcript.

All 12 variants of the regression failed before the fix because the handle was removed.

## Change

When an old result already contains a retained-output reference, its prune marker now keeps a compact read_output call with that same handle. The original result is not copied, loaded or saved again.

The extractor recognizes the bounded outer header/footer produced by the output store, including the retained attachment used for error logs and structured worker reports. It inspects at most 256 bytes at each end, validates the handle format and prefers an outer attachment over a nested preview. Unrelated text mentioning a handle is ignored.

Ordinary pruning thresholds, protected recent results and command-status preservation remain unchanged. Reference-bearing markers participate in the existing gain calculation, so the prune decision and reported token saving account for their actual size. Already-pruned history stays byte-identical.

## Verified behavior and cost

The regression covers native and thin dispatch, memory-only retention and portable persisted retention, each with:

- an ordinary large result;
- a failed operation with a retained diagnostic log;
- a structured report with retained full text.

In each variant the original operation executes once. After pruning, read_output recovers a middle-of-result detail missing from the preview. With persistence, retrieval also succeeds through a fresh loop. These are real registry/output-store and SQLite tests with deterministic model stubs, not a live-provider latency comparison.

Fixture sizes:

| Result form | Before pruning | Pruned marker after fix |
| --- | ---: | ---: |
| Large success | 4,386–4,389 bytes | 96 bytes |
| Failed operation | 2,280–2,283 bytes | 96 bytes |
| Structured report | 3,631–3,634 bytes | 96 bytes |

The previous generic marker was 48 bytes for this fixture. The fix therefore spends 48 additional bytes per affected pruned result to preserve direct recovery, while still dropping most of the preview. It does not add system instructions or automatic model calls. A model may request a stored fragment instead of repeating the original operation; this test proves that path remains usable, not that every model will choose it.

Existing bounded retention/expiry still applies. This fix does not create references for small inline results that never had one, restore references lost by an older build, or prove historical wall-time savings.

## Validation

- Red/green integration regression: 12 variants.
- Outer-reference, nested-preview, malformed-input and legacy-reference tests.
- Existing prune threshold, protected-tail, stable-prefix and exit-status tests.
- Full go test -timeout=90s ./... passes.
- go vet ./... passes.
- CLI and GUI builds pass; CLI --help smoke test passes.

Artifacts: .tmp/prune-reference-2026-09-26/{before,after,snapshot-evidence,suite,vet,build-cli,build-gui,smoke}.json. The special Zen transport is untouched; storage stays in the existing portable application layout.

Both executables were installed with previous-build backups and verified matching SHA-256 hashes; see installed.json. Running instances require a restart.
