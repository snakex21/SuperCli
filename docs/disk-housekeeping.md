# Portable-data housekeeping

Housekeeping is wired into the CLI/TUI session stack and the web Engine's lazy
session-store initialization. Merely opening a raw storage Store (tests, exports,
read helpers) does not schedule deletion. Startup schedules a timer only: the first
attempt is after 30 seconds, successful passes repeat daily, and busy/interrupted
attempts retry hourly. A pass has a two-minute cancellation deadline. Store Close
cancels and joins its task.

## Configuration

Add this optional table to **the portable data directory's `config.toml`**:

```toml
[housekeeping]
enabled = true
checkpoint_keep_per_session = 20
checkpoint_max_age_days = 14
snapshot_max_age_days = 30
snapshot_max_bytes = 536870912       # 512 MiB soft target
vacuum_interval_days = 7
vacuum_free_percent = 25
vacuum_min_bytes = 67108864          # at least 64 MiB reclaimable
```

Omitted/zero numeric settings select these defaults. `enabled = false` disables
scheduled housekeeping. Negative checkpoint keep/age disables scheduled checkpoint
expiry; negative snapshot age/bytes disables snapshot expiry; negative vacuum
interval disables periodic vacuuming. No model or reasoning settings are changed.
The scheduler intentionally reads portable/global configuration, not a project's
configuration, because the data directory is shared between projects.

## Checkpoints

The existing journaled retention collector is extended, including its durable
history-expired frontier, root revalidation, native file identity checks and
StoreGate. No broad `git gc`, repository deletion or forced recovery is added.

* Keep the newest 20 records per **existing** session, plus every record younger
  than 14 days. Both conditions protect a record, not just their intersection.
* Old records belonging to deleted sessions expire after the same 14-day grace;
  they do not receive a newest-N allowance. Missing timestamps are retained.
* A living native turn lease protects all history in its store. A stale unlocked
  lease does not by itself keep history forever; its Git recovery roots still do.
  No process-ID or age heuristic declares a lease dead.
* The existing managed pressure collector also honors these session keep/recent
  guards when a sessions database exists. Strict budget APIs retain their original
  contracts. Failure to read session/configuration evidence fails closed.
* Only unreferenced, proven loose objects can be reclaimed. Latest, index, reflog,
  unknown refs, pending recovery, packs, unsupported metadata and unknown files
  remain protected. Empty/missing-metadata repositories are **not** presumed to
  belong to a deleted session. This is deliberately not a promise of a hard cap.
* Metadata expiry and object deletions are logged; remaining mandatory graph bytes
  and unsupported stores are reported. The shared usage ledger is invalidated
  before maintenance changes it, so the next writer cannot trust stale accounting.

### Why a large protected floor can persist

The previous policy was pressure-only, not session-aware or age-aware. More
importantly, a Git index/latest/unknown ref/reflog can keep an entire old snapshot
graph reachable even after its conversation is deleted. Unsupported metadata
protects the whole repository. The managed policy permits **1 GiB plus a proven
protected floor** when that floor exceeds 1 GiB; the nominal 1 GiB target therefore
never meant that protected data would be pruned. Safe session expiry cannot turn
an independent recovery/index/unknown root into garbage.

A read-only filesystem check during implementation found approximately 874.6 MB
(decimal; 0.815 GiB) in the specified `supercli-data/checkpoints`, not the reported
9.2 GB. Four tiny stores have no `turns.json`; the largest store is about 742 MB
and has a Git index. `badcheckpoints` was empty. No collector was run on these
real directories, and no exact live reachability census was performed. Thus the
code explains the protected-floor behavior, but the historical 9.2 GB figure
cannot be reproduced or attributed byte-for-byte from this check. Unknown roots
are intentionally not deleted merely to make that reported number smaller.

## Sessions SQLite

New databases already use incremental auto-vacuum; legacy databases intentionally
remain unchanged on open. The idle task checks page_count, freelist_count and
page_size, and only reclaims when at least 25% and 64 MiB are free and the last
successful maintenance was at least seven days ago. Success time is durable in
`housekeeping_meta` inside sessions.db. Incremental mode reclaims up to 16,384
pages per pass; legacy mode uses VACUUM. A separate zero-busy-timeout connection
never queues behind another SQLite transaction. Failed/cancelled attempts do not
advance the success timestamp.

Chats have priority. All agent runs take shared native portable leases; maintenance
uses an exclusive lease. A new local run cancels maintenance immediately; another
cooperating process requests cancellation through the lease file's control byte
(checked every 20 ms), then enters after the cancelled operation releases its
lease. SQLite context cancellation interrupts the rewrite. This may require a
brief cancellation handoff, but does not queue a chat behind an entire VACUUM.
This coordination requires processes built with this implementation; older
already-running binaries cannot participate retroactively. No running executable
is restarted by this change.

## Snapshot files

Saved tool results can refer to `snapshot:<name>`, preview paths or absolute paths;
a copy in session-media **does not prove** that the original filename is unused.
Consequently capture-time copy-based deletion is no longer called. Idle cleanup
scans persisted messages (including parts and tool calls), saved tool outputs,
message attachments, queued prompts/attachments, and turn metadata for generated
snapshot filenames. Any reference, including a false-positive textual mention,
keeps the file. A failed/incomplete census deletes nothing.

Only known generated screenshot names are eligible. Unreferenced files expire at
30 days, or oldest-first under the 512 MiB soft target, always with at least a
seven-day in-flight grace. Unknown/user filenames, directories, links and recent
captures are retained. Referenced/recent files may exceed the soft target. File
identity, size and modification time are rechecked before deletion. No image
hashing is needed, and no session-media files are removed.

All deletion tests use `t.TempDir()`. No housekeeping/cleanup was exercised on the
real data directory, and no application executable was overwritten or restarted.
