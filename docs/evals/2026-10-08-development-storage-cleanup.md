# Development storage cleanup — 2026-10-08

A read-only size audit of the application checkout measured 111,594,039,937
file bytes (103.93 GiB). Developer experiments under .tmp accounted for
65,936,729,697 bytes; portable application data for 44,552,063,821 bytes.
Neighbouring original projects/mail archives were outside cleanup scope.

## Verified cleanup

| Exact scope | Evidence | File-byte result |
| --- | --- | ---: |
| Old .tmp/go-build cache hex directories | Go cache layout; obsolete driver cache, 256 guarded directories; fuzz/README/trim retained | 24,562,020,770 bytes before cleanup |
| Fixed-name old generated application builds | 895 exact regular files, size/mtime/resolved containment guards; current Oct8 builds/backups excluded | 23,962,643,726 bytes removed |
| Additional isolated stale caches/compiler work and benchmark programs | 47,327 exact metadata-manifest files, provenance-reviewed scopes; no recursive scope deletion, no database files | 4,037,509,890 bytes removed |
| Additional provenance-verified old fixtures/cache and public build archives | 3,780 exact size/mtime-guarded files, six driver/source provenance groups; uncertain data/profile directories excluded | 1,652,095,024 bytes removed |
| Orphaned loose objects in badcheckpoints/1f36b58792b492e6 | Fresh union of every before/after, all refs and HEAD; valid complete records and object headers; inert archive unreferenced by runtime; no refs/record changes | 708 files / 1,324,041,329 bytes removed |

Every deletion checked its resolved absolute target inside its explicit cache
or archive directory. Symlinks, changed sizes/mtimes and current data were
excluded. All source snapshots, overlay fixtures, reports, source code, current
builds, current Go cache and actual user projects were retained. Original
workspaces, conversations, memories and registered undo records were not deleted.
No application was closed or restarted.

The checkpoint audit covered all 50 recorded undo points in seven complete
repositories. Five incomplete/error repositories were excluded. Nonempty
reflogs or special recovery metadata would also exclude a repository. Active
checkpoint loose garbage (only 5,470 bytes) was intentionally left outside
cleanup because an active writer could create unpublished objects. Only the
old inactive archive was cleaned, after a second full-root audit and immediate
record/ref/file metadata checks. A subsequent audit retained identical protected
reachable IDs, refs and record SHA, all 50 records, and zero remaining orphaned
objects in that archive. Header completeness is not a full blob corruption test
or an actual restore of every legacy point.

## Measured remaining storage

After both current applications and all platform builds were verified/installed,
a fresh application-only traversal measured **56,616,010,474 bytes / 52.728 GiB**
over 124,617 files: **54,978,029,463 bytes / 51.202 GiB net reduction**. No access
errors were reported; one symbolic link was not followed. Directory/file lengths
are logical bytes, not a promise of the exact NTFS allocated-cluster reduction.
New cache/build output explains why the net reduction differs from gross
removed-file sums.

Before the offline narrowing pilot, retained checkpoint/archive files still accounted for about **39.72 GiB**.
Those reachable historical snapshots contain large unrelated files captured
by the old whole-workspace implementation. Their mere size is not permission
to discard a recorded restore point. The implemented scoped/bounded checkpoint
capture prevents repeating that accidental whole-project copy for a small edit;
it does not silently expire old history. Current developer Go cache is about
4.79 GB, retained warm for further central tests.

## Follow-up: whole-store budget

The snapshot's 1 GiB input limit is not a lifetime store limit. Both sides of a
turn, historical records and inactive archives can still add up to much more.
A store-wide quota and its retention policy remain pending; this cleanup must
not be advertised as implementing that quota. A read-only design audit found
that safe reclamation also needs active-turn protection and coordination
between GUI/TUI processes. Repeated per-message directory scans or automatic
Git prune without those protections would be unsafe and wasteful.

An independent read-only audit is estimating lossless narrowing of old trees:
Undo/Redo inspects only each record's Files. Retaining identical required blob
IDs, modes and absent paths can preserve those operations while releasing
unrelated snapshot siblings. Recorded IDs, user sequence, Undone and RawBytes
must remain unchanged. Active cached Managers must not be rewritten by an
external cleanup; the initial candidate is the inactive archive only.

As a prerequisite, Open now rejects malformed or unreadable turns.json instead
of silently continuing with partial or empty records. Two synthetic regression
tests preserve the bad metadata and verify that no replacement is written.
The checkpoint test package and vet passed; no real user history was changed.

Exact receipts and immutable metadata plans are in ignored portable
.tmp/optimization-oct8-2026. No secrets or chat contents are included in these
committed evaluation notes. No Git commit/push/release was made.

## Completed offline pilot — 2026-10-09

The frozen narrowing prototype passed 671 synthetic checks, including opaque
metadata preservation, exact path/OID/mode/missing parity and interrupted-process
recovery. Root then reviewed and applied it to only the inactive
`badcheckpoints/78d94e867f821f9f` archive. Both sides of its one recorded point
were verified against the original trees; every metadata byte outside the two
commit references was preserved. The legacy private index was backed up and
retired. All current record sides were durably pinned before reclaiming the
source-owned temporary latest/index/migration guards. Active repositories were
not changed.

Under the same archive OS lock, fresh actual reachability audits and exact
resolved-path/regular-file/link/size/mtime guards authorized deletion of only
**3,217 unreachable loose objects / 15,968,986,373 bytes**. A subsequent audit
retained all 50 persisted records, identical protected current roots and record
metadata, and zero remaining orphaned objects in the selected archive. Required
new-tree lookup digests were also checked after deletion. This is a preservation
claim for stored archive records, not a claim that the current GUI lists all
archived records or that every legacy blob has been restored to a workspace.
Original project files and conversations were untouched.

After new builds/fixtures and this actual reclaim, a fresh traversal measured
**40,933,056,514 bytes / 38.122 GiB**, 124,213 files, no access errors and one
unfollowed symbolic link. Net reduction from the original audit is
**70,660,983,423 bytes / 65.808 GiB**. Checkpoint/archive files remain
**26,682,691,015 bytes / 24.850 GiB**. Logical file bytes differ from allocated
NTFS space; these counts do not imply the runtime quota has been implemented.

The user delegated retention policy selection. The selected next policy is
approximately 1 GiB shared across GUI/TUI/projects, retaining newest undo points
and expiring oldest when needed. **Automatic quota/expiry remains inactive.**
The read-only audit found that retaining all 50 historical points after complete
narrowing still needs about 4.841 GiB with current loose blobs; one required blob
is over 4 GB. Current application-data filtering excludes none of those paths.
Further reduction to the selected quota requires explicit history expiry plus
reviewed cross-process gates, active roots and worker mutation lifecycle. The
prepared store/worker prototype has 11 proposed regressions that have not been
run; it is not production code. Work was stopped for today at the user request.

The malformed-metadata protection passed all 71 Go packages, vet and 215 UI
tests; all ten platform builds passed. Updated Windows TUI/GUI were installed
and checksums verified. Version remains 1.0.4. Validation source digest:
`b9d5a6ff08f98c04bcd0942b649e306834a9ba63b3b712064e485e5af2bc6c67`.
The staged full-test observer timed out without a completion log; a notification
wait and one justified check over five minutes after launch found no Go/test
process. The direct completion-logged run then passed all 71 packages. No success
was inferred from the failed observer; no overlapping test suites were launched.
No commit, push or release was made.
