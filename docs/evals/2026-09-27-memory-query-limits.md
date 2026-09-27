# Bounded memory reads with matching recency indexes — 2026-09-27

## Confirmed work amplification

List/Recent and recentByCreated selected all matching memory rows, decoded every content/tag/timestamp field and built a full Go slice before returning its first N entries. Callers include session-start briefing, prior-session summaries, autosave lookups and explicit memory listing. A request for three task logs therefore decoded every task log in that store.

SQL LIMIT alone reduced Go allocations but regressed some timings: the created-time stress case rose from 21.6–25.0 ms to 91.9–94.8 ms while SQLite maintained a temporary sort over large content rows. That intermediate variant is not shipped.

## Change

Both read orders now apply the requested limit in SQL. Zero/negative limits retain the existing unlimited behavior. Four recency indexes cover updated-time and created-time ordering, with and without an exact scope. Each includes the existing ID tie-break. They replace the older scope-only and updated-only indexes, whose prefixes the new indexes cover.

The final query plans use ordered indexes without temporary ORDER BY trees. The application decodes only selected entries. Unlimited export/maintenance still returns all entries. Migration creates the replacement indexes before dropping the superseded ones; no entry, text, FTS record or mirror is removed.

This changes local database work only. It adds no provider calls, prompt instructions, context tokens or cached/stale query results, and leaves the Zen transport unchanged.

## Measurements

Windows / AMD Ryzen 7 5800X3D; warmed local operations, three runs of 20 iterations unless stated otherwise. Synthetic fixtures use the real Store schema with 64 or 4,096 entries, about 1 KB each. The larger fixture is a capacity stress case with manually populated scopes, not a representative automatic task-log retention count. Fixture setup is excluded from read timings.

| Operation, 4,096 entries | Original | Final |
| --- | ---: | ---: |
| Six recent entries across scopes | 14.02–15.51 ms | 0.059–0.077 ms |
| Three recent task logs | 6.31–6.96 ms | 0.061–0.092 ms |
| Created-time budgeted recall | 21.58–24.99 ms | 0.130–0.144 ms |
| Project briefing | 19.90–24.82 ms | 0.235–0.255 ms |
| Allocated bytes per briefing, median | 6,533,348 B | 43,764 B |

At 64 entries, briefing falls from 0.296–0.333 ms to 0.228–0.244 ms. The reduction is much smaller than at capacity.

### Saved memories

Replayed the existing private snapshot with original IDs/scopes/content/tags/source and created/updated timestamps: 49 SuperCli entries, 59 USOS entries and 16 GunMayhem entries. The selected text, ordering and rendered-context fingerprints match before/after across all three runs.

A longer confirmation used three runs of 200 iterations with a Go overlay for the original two production files, without reverting the working tree:

| Saved project briefing | Original | Final |
| --- | ---: | ---: |
| SuperCli | 0.240–0.253 ms | 0.171–0.179 ms |
| USOS | 0.291–0.358 ms | 0.193–0.199 ms |
| GunMayhem | 0.165–0.222 ms | 0.200–0.218 ms |

The smallest snapshot does not demonstrate a time improvement and is somewhat slower in these measurements. Allocated bytes decrease for all three. This is not a claim that every small database, model response or full session is faster.

### Storage/write tradeoffs

Indexes consume space and maintenance work. The 4,096-entry fixture's logical SQLite size grows from 5,877,760 to 6,438,912 bytes (561,152 extra bytes, about 9.5%; fixture-specific). Extra indexes contain ordering keys, not duplicated content.

Measured separately against the pre-index layout:

| Operation | Pre-index | Final |
| --- | ---: | ---: |
| Reopen, 64 entries | 9.57–10.33 ms | 9.38–9.56 ms |
| Update with mirror, 64 entries | 6.47–6.65 ms | 6.95–7.58 ms |
| Reopen, 4,096 entries | 105.00–110.85 ms | 108.71–115.22 ms |
| Update with mirror, 4,096 entries | 34.95–35.64 ms | 34.21–35.99 ms |

These include normal mirror reconciliation/writes. They are not initial migration timings. Small writes show a modest added cost; large updates are comparable. The tradeoff removes read work that previously grew with the whole matching history.

## Verification

- Ordered entry equality against an independent sort/filter reference: all/scoped/missing scopes, literal SQL-like scope text, zero/negative/positive limits, stable timestamp ties, full metadata and fresh writes.
- Opt-in saved-data replay checks ordering/content and emits only fingerprints/counts, not private text.
- Migration from the old index layout preserves saved records, FTS search, subsequent writes and deletion.
- Full go test -timeout=90s ./..., go vet ./..., CLI/GUI builds, CLI --help and git diff --check pass.
- Both executable copies installed with backups and SHA-256 verification.

Artifacts: .tmp/memory-query-limit-2026-09-27/ contains the original/limit-only/indexed measurements, query plans, storage costs, saved-data confirmation, baseline Go overlay, full checks and installation records. The private source snapshot stays in .tmp/memory-recall-filter-2026-09-27/saved-memories.json.
