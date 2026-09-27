# Patch hash description experiment — 2026-09-26

## Question and saved-session evidence

Could a shorter, clearer description of optional base_hash prevent models from launching a separate checksum command before edits?

The fixed session snapshot contains 45 patch_file calls (30 supplying base_hash) and 38 Get-FileHash-related command calls. Sixteen of those commands returned at least one digest previously present in a successful patch result. Some commands process multiple files or inspect binaries/signatures; intervening work and external edits can justify refreshing a digest. Therefore this is not a count of 16 redundant commands, nor evidence that all 38 can be eliminated.

Targeted reads stop at the requested range rather than loading the whole file. Making every read compute a whole-file digest would add I/O and repeated metadata to otherwise cheap reads, so it was not added.

## Controlled model experiment

Candidate description: "Optional known SHA-256; rejects stale edits".
Existing description: "SHA-256 of current contents; rejects stale edits".

The tool schema already makes base_hash optional. This experiment changed only that field's description in the test registry, never the production tool. Two consecutive edits changed a four-line retry.go fixture from MaxAttempts=2 to 3, then to 4. After the first edit, the normal patch result supplied an after_hash available for reuse.

Both models used the existing native tool route, low reasoning effort and the actual coordinator Loop. Local Qwen used temperature 0 and seed 20260926. Read and patch tools operated on real files in temporary isolated directories. ctx_execute was a checksum-only fixture and never launched a process. No user files or private logs were sent to providers.

| Model | Description | Hash requests served | Model calls | Input tokens | Output tokens | Seconds | Both file states correct |
| --- | --- | ---: | ---: | ---: | ---: | ---: | --- |
| Qwen3.8-27b-uncensored | Existing | 0 | 8 | 16,685 | 1,399 | 46.03 | Yes |
| Qwen3.8-27b-uncensored | Candidate | 0 | 9 | 18,744 | 1,246 | 41.39 | Yes |
| Muse spark 1.3 contributor free | Existing | 0 | 10 | 27,379 | 1,811 | 26.12 | Yes |
| Muse spark 1.3 contributor free | Candidate | 1 | 9 | 24,079 | 2,000 | 28.33 | Yes |

The candidate did not reduce checksum requests. Qwen reused the previous after_hash with the existing description; the candidate omitted it on the second edit. No production description or guard behavior was changed.

## Limits

This was a narrow exploratory probe, not a general editing benchmark. The checksum-only command fixture rejected unrelated discovery/build/verification requests, which several model runs attempted. Those artificial failures confound total calls and timings, so these figures must not be advertised as real-world speed comparisons. The served hash in the Muse candidate run was part of a mixed command, not an isolated checksum. All final file contents were verified, but that does not make every model action efficient.

A different fixture that can safely fulfill the normal discovery/verification workflow would be needed for representative end-to-end timing. There was insufficient benefit here to justify shipping the wording change or adding hash metadata to all reads.

## Reproduction and artifacts

Opt-in test: TestPatchHashDescriptionAB_Live in internal/agent/patch_hash_description_live_test.go. Ordinary tests skip it without SUPERCLI_PATCH_URL, SUPERCLI_PATCH_MODEL and an absolute SUPERCLI_PATCH_OUT. Artifacts must be placed under the application directory.

Supported endpoints: http://127.0.0.1:1234/v1 and https://opencode.ai/zen/v1 with a model ending in -free. SUPERCLI_PATCH_ORDER controls legacy,optional or reversed order. SUPERCLI_PATCH_ROUTE=thin selects text routing for future probes; it was not tested in this batch.

Run: go test -count=1 -timeout=13m ./internal/agent -run '^TestPatchHashDescriptionAB_Live$' -v

Inspect TurnCorrect, Error and the tool-call list in each saved JSON. Exit status alone is not a quality assertion; Error records loop-level errors, not every rejected tool call. Each turn has a 180-second bound and six loop steps.

Local results are under .tmp/patch-hash-description-2026-09-26/local/ and zen/. The experiment led to source review and a separate [confirmed empty-file mutation fix](2026-09-26-empty-file-mutation.md), not a claim of hash-related speedup.
