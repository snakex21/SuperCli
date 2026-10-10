# Session FTS external-content migration — 2026-10-08

## Evidence and scope

A read-only aggregate audit of the portable `sessions.db` found 18,931 rows in
both `messages` and `messages_fts_content`. Each held 23,255,313 bytes of indexed
content. Comparing row IDs and `COALESCE(content, '')` as BLOBs found no missing,
orphaned or different rows. The old FTS table therefore duplicated existing
message text; this conclusion did not come from its table name alone.

The production change indexes the same `messages.content` through
`content='messages', content_rowid='id'`. It retains
`porter unicode61 remove_diacritics 2`, default detail/docsize, snippets, rank,
BM25, filters and search limits. `parts_json` and `tool_calls_json` remain outside
this index. Messages, session metadata, paged history and media are not rewritten
or removed. No production data was migrated or compacted during this evaluation.

## Driver experiment

The isolated synthetic experiment is in
`.tmp/optimization-oct8-2026/fts-experiment/fts_experiment_test.go`. It uses the
repository's pinned `modernc.org/sqlite v1.52.0`, reporting SQLite **3.53.2**.
Completed runs are recorded in
`.tmp/optimization-oct8-2026/go-fts-experiment-{0,1}.log`.

The fixture has 10,000 messages and 23,387,399 bytes of content, including empty
assistant content, longer tool output, user/system messages and nullable content
cases. Tests cover insert/update/delete, transaction rollback, cascade delete,
rebuild, external-content integrity checks, MATCH ordering, rank/BM25/snippets,
workspace/session grouping and paged history. Fresh and migrated fixture sizes
were measured after **fixture-only VACUUM**, with `dbstat` page accounting.

| Measure | Internal content | External content |
|---|---:|---:|
| Fresh fixture database after VACUUM | 53,981,184 B | 28,880,896 B |
| Migrated fixture after VACUUM | — | 28,852,224 B |
| History search median | 86.959 ms | 84.344 ms |
| Search allocated bytes/operation | about 350,300 B | about 338,965 B |
| Grouped search median | 36.214 ms | 35.406 ms |
| Paged history median | 0.356 ms | 0.345 ms |
| Paged history allocated bytes/operation | 269,969 B | 269,969 B |
| Create and populate 10k messages median | 1.119 s | 0.824 s |
| One-time migration median | — | 0.553 s |

Timing medians use three benchmark samples. The fresh size reduction is
25,100,288 bytes, or 46.5% of this fixture's whole database. Actual application
databases contain other tables, so this percentage is not a whole-store forecast.
These are allocation and file/page measurements, not process RSS measurements.
The change does not reduce model prompt tokens or guarantee that SQLite releases
an equivalent amount of resident memory.

## Deletion semantics and the historical comment

An external-content FTS table must remove old tokens using
`INSERT INTO messages_fts(messages_fts,rowid,content)
VALUES('delete',old.id,COALESCE(old.content,''))` in AFTER DELETE/UPDATE triggers.
The source row has already disappeared or changed when those triggers run.
SQLite documents the need to supply the old indexed values and to backfill
pre-existing rows with `rebuild`.
([SQLite FTS5 external-content tables](https://www.sqlite.org/fts5.html#external_content_tables))

The negative control using the former ordinary `DELETE FROM messages_fts` in
an AFTER DELETE trigger returned no SQL error but left an orphan index entry;
the subsequent external integrity check failed with malformed-index error 267.
The canonical OLD-content deletion passed with the pinned driver. This does not
support the old source comment claiming that `content_rowid` itself is broken.
The recovery commit available in repository history already contains that
comment; the original failing statement is unavailable, so its exact historical
failure is not claimed to have been reproduced.

## Production transaction and startup behavior

`Store.migrate` delegates to `ensureMessagesFTS`. A normal start reads four schema
definitions and returns when the external table and all three canonical triggers
are present. It does not rebuild or integrity-check the full index on every
start. SQL formatting/keyword case differences are ignored; quoted option values
remain exact. Unknown table/tokenizer/column definitions are rejected with a
descriptive error rather than replaced by a guessed schema.

A missing index, the known internal-content schema, or canonical external schema
with missing/obsolete triggers takes this one-time repair path:

1. Pin one SQL connection and `BEGIN IMMEDIATE` before re-reading the schema.
2. Re-check after acquiring the writer, since another opener may have migrated
   while this one waited. An already-current schema requires no replacement.
3. Drop old triggers/table, create the external table and canonical triggers,
   rebuild from authoritative `messages` rows.
4. Run `integrity-check` with `rank=1`, which compares the index to its external
   source, then commit.

The writer reservation prevents messages from changing between rebuild and
verification. Table, triggers and index replacement are transactional. A create,
rebuild, verification or commit error rolls back the entire FTS replacement.
Failed connections are discarded and explicitly closed before another connection
is acquired, including when the pool has just one slot. The measured approximately
0.553-second rebuild for 10k synthetic rows motivates a one-time migration,
while the metadata-only fast path avoids this cost on subsequent starts. Other
workloads and concurrent writers may change latency; the existing five-second
busy timeout remains in effect.

The optimization has a narrow availability fallback after a migration failure:
it requires explicitly confirmed `ROLLBACK`, confirmed disposal of the failed
connection, and the exact original internal table with all three exact original
triggers. A different connection reserves the writer with `BEGIN IMMEDIATE`,
checks that schema again, compares every indexed row ID/content with `messages`
in both directions using BLOB equality and NULL-to-empty normalization, and runs
the old index's integrity check. Internal FTS integrity alone would not compare
its duplicate source text with `messages`, so both checks are required. Only a
fully consistent legacy store is retained, with one warning that optimization
was postponed. History searches and the conversation writer remain available;
the next open retries the migration. The slow full verification is confined to
this exceptional failure path.

Missing/unknown schemas, missing or altered legacy triggers, inconsistent
content/indexes, unconfirmed rollback/disposal, or verification failures remain
hard errors. In particular, a failed `BEGIN IMMEDIATE` without a confirmed
rollback does not qualify. No empty or partially rebuilt index is published.
For these hard errors, caller behavior remains unchanged: the WebGUI returns the
store-opening error; the CLI's existing `openSessionStack` logs it and disables
the session store/search writer while allowing the chat to continue in memory.
The optional migration fallback avoids this CLI behavior only where preserving
the old store has been positively verified.

No VACUUM or forced WAL checkpoint runs on startup. Dropped duplicate-content
pages become reusable; SQLite does not immediately shrink the physical database
file. The fixture's compacted sizes describe potential storage savings after
separate maintenance, not a promise that the user's file shrinks at migration.
([SQLite VACUUM](https://www.sqlite.org/lang_vacuum.html))

## Compatibility, failure coverage and rollback

`store_fts_test.go` exercises the production store with the pinned driver:

- Fresh external schema, no duplicate content shadow table, stemming/diacritics,
  content-only indexing, content/row-ID edits, NULL/empty transitions, transaction
  rollback, truncation, branching and cascaded session deletion.
- Opening the exact former internal table/triggers preserves history, MATCH
  result ordering, numeric rank/BM25, snippets, grouped search and history pages.
- Opening a missing index backfills existing messages instead of publishing an
  empty index; missing/obsolete external triggers rebuild and repair orphan tokens.
- A real candidate index is deliberately given an orphan entry immediately
  before the real rank=1 integrity check. Failure restores the legacy table,
  triggers and search results; old triggers still work, and a retry succeeds.
- The optional migration failure retains a verified old store even with a
  one-connection pool. Its real conversation writer persists new messages and
  search/deletion work; the next open upgrades without changing those messages.
  Missing triggers/indexes, missing/orphan/different content, corrupted inverted
  index leaves and an explicitly failed rollback cannot activate fallback.
- Unsupported tokenizer definitions fail both direct migration and `OpenStore`
  without changing the original index or history. Writer reservation precedes
  the schema re-check, and an already-current table avoids replacement.
- The former F13 bootstrap's existence check leaves the external table/triggers
  untouched. A separate old-style writer then inserts, edits and deletes source
  rows and deletes a session successfully. Older application code uses the same
  table name and APIs; triggers own the FTS synchronization.

Source was formatted with gofmt. Production package checks/builds are performed
centrally by the parent task; this agent did not run Go tests/builds or operate
on user databases.

Failure rollback is automatic before commit. Application rollback to the former
binary also preserves the migrated schema because its bootstrap checks table
existence. If a manual schema downgrade is ever required, it must reserve the
writer, atomically install the former internal table and triggers, and backfill
with `INSERT INTO messages_fts(rowid,content) SELECT id,COALESCE(content,'') FROM
messages` before validating/committing. Running `rebuild` on a newly empty
internal-content table cannot recover messages from its external source. No
downgrade or compaction action is included in ordinary startup.

## Consistent copy of the real portable database

The final integration run used `VACUUM INTO` from a read-only connection to the
original store. Only the isolated, path-checked copy was opened with the
production `session.OpenStore`; no original data, schema or file was changed.
The helper streamed SHA-256 over every column of every message and session,
checked SQLite integrity and external FTS integrity, and repeated these checks
after copy-only VACUUM. The completion log is
`.tmp/optimization-oct8-2026/go-production-measurements-3.log`; aggregate results
are preserved in `fts-actual-metrics.json` there.

| Measure | Result |
|---|---:|
| Sessions, before / after / compacted copy | 201 / 201 / 201 |
| Messages, before / after / compacted copy | 18,931 / 18,931 / 18,931 |
| Stored history payload, each stage | 53,287,886 B |
| Consistent copy before migration | 120,098,816 B |
| Copy after migration, before VACUUM | 120,098,816 B |
| Reusable pages after migration, 4 KiB/page | 6,716 |
| Copy after VACUUM | 92,581,888 B |
| Space recovered by copy-only compaction | 27,516,928 B |
| One-time production migration | 1,516 ms |

All stages preserved the same full-history digest:
`bf4ef84ea309b3032405992c901e9003186fd2e5f00ff07583e72988d6930385`.
The original file was 121,741,312 bytes; creating the consistent copy also
removed its existing unused pages, so that original-to-final difference is not
attributed solely to FTS. No process RSS or model-speed improvement is claimed.
The temporary private-history copy was deleted after preserving these aggregate
results; the original store and its history remain intact.
