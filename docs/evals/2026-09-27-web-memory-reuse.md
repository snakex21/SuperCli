# Reuse GUI memory connections — 2026-09-27

## Evidence and change

The GUI tool adapter opened and closed a memory Store for every remember/recall operation, even when the engine already owned the same database for briefing and session capsules. A fallback recall opened each store twice. The deterministic user-fact saver also reopened global memory before every chat request, including prompts containing no personal facts.

OpenStore performs SQLite setup/migration and Markdown mirror reconciliation. The mirror invariant test passed before the change: unchanged files were not physically rewritten. The measured overhead is repeated initialization/reconciliation, not proof of repeated disk writes to every Markdown file.

The adapter now borrows the existing lazy engine-owned stores. The user-fact saver uses the same global connection. Failed opens are retried; a closed engine cannot reopen a store. The keeper captures the request workspace so switching the active project cannot redirect an older run's memory operations. Engine.Close already closes these stores after HTTP drain.

Database values are not cached in Go: committed edits and deletions from independent Store connections remain immediately visible. Existing memory-management HTTP handlers retain their current explicit store lifecycle. CLI memory already uses retained stores and needs no new mechanism. No provider-specific path, permanent instructions, model calls or data directories changed.

## Local measurement

BenchmarkWebMemoryAccess uses the actual GUI registry with seven synthetic global entries and 49 project entries across fact/task-log/pattern scopes. The stores are already warm, as after a briefing. Each result below covers three runs of 20 operations on this Windows host; fixture creation is outside timing.

| Operation | Before | After |
| --- | ---: | ---: |
| recall with lexical hits | 15.39–15.81 ms | 0.387–0.411 ms |
| recall with recent-note fallback | 28.79–29.37 ms | 0.420–0.454 ms |
| user-fact saver, prompt without a fact | 7.71–7.89 ms | 0.000535–0.000550 ms |

Allocation counts per operation fell from about 5,280 to 449 for a hit and 10,220 to 662 for fallback. The first lazy open and genuine fact writes still do their required work. These measurements concern local bookkeeping only; no full-session or provider-latency improvement was measured.

## Validation

- Red/green ownership test: tools previously reopened engine-owned stores; now both project/global pointers are reused.
- Existing recall-filter and GUI registry tests pass.
- Read-only mirror timestamps remain unchanged (this invariant also passed before).
- Twelve concurrent cold opens/writes produce one Store per scope and retain all writes.
- External writes/deletions remain visible; captured workspaces remain isolated after the active workspace changes.
- Deterministic personal facts persist and are readable through an independent database connection.
- Failed open is retried after the directory is repaired.
- Engine close releases the databases and subsequent tool access fails rather than reopening them.
- Full go test -timeout=90s ./..., go vet ./..., both executable builds, CLI --help and git diff --check pass. Concurrency tests are ordinary tests, not a race-detector run.
- CLI and GUI binaries installed with backups and SHA-256 checks.

Artifacts: .tmp/web-memory-reuse-2026-09-27/ contains the before/after benchmarks, baseline invariants, red ownership and green lifecycle results, full checks and build/installation records.
