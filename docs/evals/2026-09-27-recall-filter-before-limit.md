# Recall filters before result limits — 2026-09-27

## Confirmed defect

The recall tool filtered legacy diagnostic patterns only after Search/HybridSearch had selected the first k entries. A useful entry below that cutoff could never reach the model. If every selected match was diagnostic noise, recall substituted recent unrelated notes even though useful lexical matches existed. The recent-note fallback had the same ordering defect: it fetched at most 4*k entries and then removed all pattern entries.

Read-only inspection of the portable databases found this in current saved data. A private snapshot of 124 entries from SuperCli, Universal_Service_OS and GunMayhem was imported into isolated temporary stores to exercise the real recall tool. No live database was edited and no memory content is included here.

At limit 5, useful lexical matches present in the final recall response:

| Saved database | Query | Before | After |
| --- | --- | ---: | ---: |
| SuperCli | model | 0 / 4 | 4 / 4 |
| SuperCli | error | 4 / 5 | 5 / 5 |
| Universal_Service_OS | model | 2 / 5 | 5 / 5 |
| Universal_Service_OS | error | 3 / 5 | 5 / 5 |

The unchanged GunMayhem queries returned 2/2 useful matches before and after. These are deliberate replay queries against saved data, not proof that these exact queries occurred in an earlier session or caused its elapsed duration. Synthetic fixtures also reproduce an older fact hidden behind 45 newer patterns at limits 1, 5 and 10.

## Change

- Persistent stores provide RecallSearch and RecallRecent. CLI tools use them directly; the GUI keeper forwards to the same implementation.
- RecallSearch excludes the already-recognized legacy “no heuristic matched” pattern before FTS/LIKE limits and before vector candidate ranking/caps. The same hybrid fusion and embedding-error fallback remain in use.
- RecallRecent selects non-pattern entries before its SQL limit. The existing preference/profile prioritization and final tool-output bounds stay in place.
- Useful matching patterns remain available. Public Search, HybridSearch and Recent retain inspection semantics; nothing is deleted from the database or Markdown mirror.
- Other MemoryKeeper implementations retain their existing compatibility fallback. The corrected pre-limit policy is supplied by the persistent store and GUI adapter used in production.

There is no new provider call, tool definition, permanent instruction, data location or Zen transport change. The optional query embedder is called once, as before. The result may contain more useful context because entries that were incorrectly hidden are now returned; this is not a claim of fewer tokens in every request.

## Verification

- Red/green TestRecallFiltersBeforeResultLimit: project/global stores, lexical match and cross-language fallback, limits 1/5/10.
- Red/green TestRecallSavedMemoryRanking: exact saved content/tags/scopes, same FTS ranking, seven nonempty query cases across three snapshots. Private replay data stays under .tmp/memory-recall-filter-2026-09-27/saved-memories.json and is opt-in with SUPERCLI_RECALL_SNAPSHOT.
- TestRecallSearchFiltersLexicalCandidates: normal FTS, invalid-FTS LIKE fallback, unavailable optional embedder, useful diagnostic retention, general inspection search.
- TestRecallSearchFiltersVectorCandidates: closer noise vectors cannot displace the two useful entries; exactly one query embedding; unfiltered HybridSearch still exposes diagnostics.
- TestRecallSearchPolicyMatchesDiagnosticNoise: mixed-case diagnostic text, scope boundaries, ordinary facts with similar text, fallback exclusions.
- TestWebRecallFiltersBeforeLimit: actual GUI loop registry, project/global stores, lexical and recent fallback paths.
- Full go test -timeout=90s ./..., go vet ./..., CLI and GUI builds: passed.
- CLI --help and git diff --check: passed. Both executable copies installed with backups and SHA-256 verification.

Artifacts: .tmp/memory-recall-filter-2026-09-27/ (red, green, focused and full validation results, private snapshot, binaries and previous executable backups).

No live-model turn savings or end-to-end latency improvement was measured. This removes an observed retrieval defect that can otherwise require another search or produce unrelated context.
