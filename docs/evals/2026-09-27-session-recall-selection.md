# Select project conversations before the history limit — 2026-09-27

## Confirmed defect

The GUI's older-session fallback took 32 globally ranked message hits, then removed the current session, other projects and duplicate session IDs. A foreign/current conversation or many matches from one long conversation could fill the entire window. Useful prior conversations in the active project then disappeared from the context addon.

Read-only inspection of the saved sessions database (157 recorded sessions at inspection) found this with representative test/build/model/error queries. An exact copy of the new production SQL was replayed in one read transaction against that database, using Windows-normalized workspace allowlists. Fifteen project/query combinations recovered previously omitted conversations. Examples:

| Project / query | Before | After |
| --- | ---: | ---: |
| GunMayhem, test | 0 / 2 conversations | 2 / 2 |
| GunMayhem, build | 0 / 2 | 2 / 2 |
| Universal_Service_OS, model | 2 / 4 | 4 / 4 |
| Universal_Service_OS, test OR build | 2 / 4 | 4 / 4 |

These are deliberate replay queries against stored data, not proof that a historical model issued those exact queries or that this caused a particular session's elapsed duration. Only aggregate counts/workspace fingerprints were saved in the audit; no live session was modified.

## Change

The shared session store supplies recorded workspace names and a search returning one best matching message per conversation. The GUI resolves workspace aliases with its existing identity check, excludes the current session in SQL and requests four distinct conversations. It no longer performs a separate session-metadata read for every candidate hit.

The SQL materializes compact match metadata, ranks within each session and creates excerpts only for selected messages. It still scans/ranks matching rows within the allowed projects; this is not a constant-time search guarantee. General SearchHistory remains unchanged for explicit user/tool searches. The recent-history fallback, existing 420-token addon budget, message-tail limits and capsule-first path are unchanged. No model call, instruction, provider route or storage location was added.

The nearby capsule-selection hypothesis was also checked on the earlier fixed memory snapshot: the sampled queries showed no missing task-log candidates, so that path was left unchanged.

## Verification

- Three red/green GUI regressions: a foreign conversation filling the old window; the current conversation filling it; one prior conversation with 48 repeated matches crowding out the other prior decisions.
- Store tests: exact workspace allowlist, one result per session, best/latest hit and snippet, result limit, empty allowlist, unknown legacy workspace, malformed queries, cancellation, unchanged general history search.
- GUI tests: Windows case/separator/dot aliases, exclusion of a nested different workspace, exclusion of the current session, unchanged output budget.
- Existing session-capsule and prior-session integration checks pass.
- Existing BenchmarkWebLegacySessionRecall, three runs of 20 operations: 1.28–1.36 ms before, 1.15–1.42 ms after. This is comparable local time, not a measured speedup. The fixture has 100 sessions and measures the full fallback retrieval.
- Full go test -timeout=90s ./..., go vet ./..., CLI/GUI builds, CLI --help and git diff --check pass.
- Both executable copies installed with backups and SHA-256 verification.

Artifacts: .tmp/session-recall-selection-2026-09-27/ contains red/green checks, aggregate saved-data replay, the exact production SQL, before/after benchmark results and build/installation records. The initial storage test fixture was corrected to represent an old unknown-workspace row directly; current public creation correctly rejects missing session metadata and an empty cwd.

No real-model turn reduction or end-to-end latency gain is claimed. The benefit is recovering relevant prior work inside the existing bounded context instead of requiring the agent to rediscover it.
