# Checkpoint storage regression, 2026-10-08

The previous implementation ran `git add -A` over the complete workspace before
the first mutating tool and once after the turn. One small edit therefore copied unrelated source
archives, Git bundles, VM/build payloads and video files into the private bare
repository. Interrupted initial captures could leave very large staging indexes
and loose objects without any completed turn. Git clean filters also created an
additional LFS object store. Parentless snapshot commits had only a latest ref,
so older undo states were not protected from Git maintenance.

A read-only audit of existing application data measured:

| Directory | File bytes | Completed records |
| --- | ---: | ---: |
| `checkpoints` | 28,299,232,091 | 3 |
| `badcheckpoints` | 39,477,620,324 | 47 |
| Total | 67,776,852,415 | 50 |

The largest unfinished active staging repository held 19,713,426,652 bytes and
526,784 index paths, including 526,603 source-tree paths, but no records and no
Git refs. Its largest archived files were approximately 1.56–3.78 GB. Two other
unfinished repositories without records or refs held 2,988,734,997 and
1,098,975,530 bytes. Those three directories are independent of recorded
undo/redo history; all recorded histories should be retained. No actual user
data was changed by the audit or regression tests.

The audit used directory entries and file sizes, `turns.json` structural
metadata, `git for-each-ref`, tree/index metadata and `git count-objects`.
It did not read workspace file contents, credentials or conversations. Index
enumeration was streamed after the unfinished index exceeded a 32 MiB diagnostic
buffer, and filesystem traversal kept only a fixed number of largest entries.

## Implementation and limits

- File tools snapshot their actual affected paths. Moves/copies resolve the
  destination child when the requested destination is a directory. Office edits
  include their adjacent `.bak`; `web_download` includes its destination.
- Expanding the scope preserves each path's earliest before-state, including
  paths that originally did not exist. A later arbitrary command extends that
  baseline to the workspace without overwriting earlier before-states.
- Application data and checkpoint/Git internals remain excluded.
- Unknown command scope is bounded to 20,000 enumerated paths, 256 MiB per file
  and 1 GiB for the complete turn. Rejected preflight creates no blobs and stops
  the tool before mutation. If a command creates oversized output, completion
  reports a checkpoint error after the command's changes.
- File enumeration uses fixed batches or a capped stream. Raw blob content is
  streamed in bounded chunks and limited to its preflight size. Growth or
  concurrent changes fail the capture and remove the temporary blob.
- Raw snapshots preserve bytes, including CRLF and binary files, and execute no
  clean filter, LFS operation, or encoding/line-ending conversion. Private indexes
  and temporary blobs live inside the portable application data directory.
- Identical snapshot trees yield identical commits. Each retained record pins
  both its before and after commit. Forgotten records release their pins;
  checkpoint objects are not automatically pruned, and no history is silently
  expired or truncated.
- New records identify their byte-preserving snapshot format. Ordinary legacy
  snapshots remain usable. Proven legacy CRLF normalization or an LFS pointer
  cannot recover original bytes, so restoration reports the limitation before
  changing any workspace file; it does not execute an old filter or silently
  convert content.
- Existing loose blobs are identified by a first raw hash pass and are not
  recompressed. New blobs are written in a second validated pass; concurrent
  content/length/metadata changes abort promotion of the temporary blob.
- Restore reads raw `git cat-file blob` output through a fixed 32 KiB Go buffer
  into an adjacent temporary file. Git itself uses its blob streaming path
  ([Git implementation](https://github.com/git/git/blob/master/builtin/cat-file.c)).
  Size and object hash are verified before atomic replacement. All target files
  are staged before any existing workspace file is changed.
- Restore checks conflicts and legacy conversion before staging, then rechecks
  after staging and before each replacement. Later symlink/junction ancestors
  are rejected for staging, promotion, rollback and temporary cleanup. A failed
  replacement or metadata save rolls already-applied files back from the
  expected immutable blobs, while preserving any newer manual edit.
- Restore holds the manager lock through the operation so concurrent checkpoint
  forgetting cannot invalidate the selected record index. Metadata temporary
  files are removed when persistence fails.
- Recovery of a partially applied record or batch uses a cancellation-independent
  context. Forward undo/redo remains cancellable, while cancellation cannot
  prevent the already-applied part from being rolled back.

## Regression validation

`snapshot_test.go` exercises:

1. Repeated small edits and newly created files beside an unrelated file larger
   than 256 MiB; the checkpoint repository must remain below 128 KiB, and undo
   restores the first original state rather than an intermediate edit.
2. Scope expansion from file tools to an arbitrary command, retaining original
   content and an originally absent path.
3. Editing then moving a directory into an existing destination, including a
   `.gitignore`-excluded backup, without snapshotting the destination's siblings.
4. Full-capture refusal before tool mutation and before any object write.
5. Exact CRLF undo/redo despite configured Git text normalization and a required
   clean filter.
6. Undo/redo of older turns after pruning an isolated test repository.
7. Binary download replacement with destination-only undo/redo.
8. Explicit ignored paths both before and after a full command snapshot, while
   files created by the command keep an originally absent baseline.
9. A genuine legacy `git add` CRLF fixture refuses byte loss despite a newly
   configured required clean filter; ordinary legacy binary undo/redo still
   succeeds.
10. A 32 MiB binary restore preserves its exact SHA-256 while allocating less
    than 8 MiB of Go heap, catching the former whole-blob buffering path.
11. A second-file replacement failure and a metadata persistence failure both
    restore earlier files and leave the checkpoint's undo flag unchanged.
12. A later ancestor symlink/junction cannot redirect restore to an outside
    directory even if the outside file has matching expected bytes.
13. Replacement paths already present in a baseline consume the aggregate
    snapshot budget once.

`BenchmarkCheckpointStreamingRestore` compares 1 MiB and 64 MiB payloads and
reports allocations plus throughput. Its fixture setup is outside the measured
loop, and its restore/hash loop never reads whole binary files into Go slices.

The existing checkpoint suite also covers conflict refusal, whole-turn rewind,
rollback of partially applied batch restores, binary create/delete/modify,
application-data exclusion and persisted metadata. Test and build execution is
coordinated by the parent task; this source task ran formatting only. The parent
reported that the checkpoint tests passed in 34.5 seconds before the streaming
restore extension, and the agent/app/webgui integration suites also passed.
The complete streaming checkpoint suite then passed in 41.464 seconds, before
the final two cancellation-independent batch recovery calls were added.
The parent task records final validation of the complete change.

Central validation subsequently passed `go test ./...` and `go vet ./...`.
The final checkpoint package completed in 40.943 seconds. The streaming
undo/redo benchmark allocated 768,544 B for a 1 MiB fixture and 766,376 B for
a 64 MiB fixture (Go 1.26.2, Windows/amd64, GOMAXPROCS=2). Fixture setup and
child Git process memory are excluded; these figures measure complete
roundtrip Go allocations, not overall RSS. The large-binary regression also
checks SHA256 equality and a heap delta below 8 MiB for a 32 MiB restore.
