# Managed checkpoint history target

`CompleteManagedLocked` and `EnforceManagedStoreBudgetLocked` implement an
explicit exception for a store with a large protected floor. They preserve the
strict `CompleteLocked` and `EnforceStoreBudgetLocked` APIs.

The default base target is `DefaultStoreBudgetBytes` (1 GiB). The final measured
files covered by the collector must fit the base target, unless a complete
root classification proves that protected files alone exceed that target. In
that case the effective limit is **base + proven protected floor**. This leaves
at most the base target for physically reclaimable files above that floor at
the census. Addition overflow is an error. A floor at or below base grants no
exception.

This policy is not a strict 1 GiB limit for the whole application data folder,
nor a bound on peak disk usage. The scope is `checkpoints` and `badcheckpoints`,
plus the collector's existing reserved budget-control prefix. The administrative
`.checkpoint-usage.json` protocol is bounded to 256 bytes beside those folders
and remains outside the measured scope. Active snapshots and temporary capture
or recovery artifacts stay protected. Capture limits and the existing bounded
collector staging limit apply separately.

## Integration contract

The caller holds the portable `StoreGate` across every `Locked` method. No
helper takes a nested gate. After releasing/draining the turn's active capture
pins and recovery work, call:

```go
completion, err := counter.CompleteManagedLocked(ctx, base,
    func(ctx context.Context) (StoreBudgetResult, error) {
        return EnforceManagedStoreBudgetLocked(ctx, dataDir, base)
    })
```

Checkpoint metadata already atomically published remains committed if this
post-turn accounting/collection fails. Report that error independently; do not
rerun the mutator, recapture a completed turn, or claim that the target was met.
The callback receives the same held gate and must not invoke Manager methods
that acquire it. No context, callback, record list, graph, audit or background
worker is retained after completion.

The counter persists only the last complete-census floor allowance alongside
the existing upper byte bound and dirty generation. Known bounded writes use
the existing durable `BeginLocked`/`FinishLocked` receipt and add actual new
compressed object bytes plus conservative metadata/ref/tree growth. New object
deduplication adds zero. A write error, interrupted receipt, unknown write,
Clear, Forget, or untracked layout/root mutation must keep the counter dirty.
Clear/Forget must invalidate even if they reduce physical usage. A dirty
completion recomputes the floor before granting a new exception.

The persisted allowance is the last complete proof; it can outlive a root
until invalidation or pressure. It is not a claim that every byte is currently
protected or that all retained files are undo history. Public reporting should
show the base target, protected allowance and effective limit separately.
Writers from older binaries that do not cooperate with the gate/dirty protocol
require explicit invalidation before trusting the counter.

## Work and failure boundaries

- Each clean completion in a verified process reads the bounded counter only.
  It performs no filesystem census, Git command, JSON write, timer or polling.
- The first completion in each process, dirty/missing/malformed state, unknown
  growth and `Upper > effective limit` invoke collection. The process guard is
  a fixed 256-slot structure; collisions can cause additional collection.
- Collection first measures physical files with the existing bounded walker.
  A total at or below **base** requires no Git/root classification and clears
  any former allowance. An old cached larger limit is never used for this exit.
- Above base, the same measured audit is classified once. The exception and
  oldest eligible expiry plan are computed from that audit; changing the
  planning limit does not launch another pre-expiry root census.
- Actual metadata/ref expiry uses the existing interruption-safe journal and
  permanent rewind frontier. A fresh root census after those mutations precedes
  deletion. Only freshly proved unreferenced regular loose objects are removed,
  including native Git read-only files on Windows. Unknown roots/files,
  active leases, indexes, reflogs, packs, hard links and archive audit artifacts
  remain protected.
- Final usage credits only confirmed deletion sizes from the fresh local audit.
  If expiry lowers the floor to or below base, the exception disappears. That
  same fresh audit is replanned, with at most 32 bounded passes; failure leaves
  the counter dirty. There is no optimistic success above the fresh limit.
- An incomplete/failed callback, overflow, unsafe result, canceled context or
  generation mismatch cannot publish a clean baseline. Above base, a physical
  count without complete classification cannot reset the former floor to zero.

## Validation to run centrally

The new source tests cover 100 clean completions with zero collection calls,
bounded write growth, pressure, explicit dirty invalidation, shrinking floors,
addition overflow, fresh-process collection, a worst-case scalar JSON under
256 bytes, and strict API compatibility. Native synthetic repositories cover
actual measured usage at or below the effective limit, oldest expiry with exact
retained undo/redo, preservation of fixed artifacts and rewind frontiers, and
the transition from a one-byte over-base floor to a strict base limit after
expiry. The benchmark measures gate acquisition plus the actual bounded ledger
read for a clean completion with a protected floor.

No Go tests or builds were run by the helper's author; central validation owns
the execution and measurements. No real user data or repositories were touched.

Manager metadata reads and admissions now share the collector's 16 MiB document
bound. The bounded reader refuses oversized regular files before Open, then
checks the opened file and limits a possibly growing read to maximum + one
byte. A failed read leaves the existing in-memory list unchanged and returns a
mutation error; it does not convert legacy history to an empty list or overwrite
the file. Read-only UI methods retain their existing nil/empty-on-error behavior.

Before MarshalIndent, an allocation-free lower bound counts encoded string
contents in every Record, including both Files and Changes. It accounts for
HTML escaping, quotes, backslashes, controls, UTF-8 replacement and U+2028/U+2029.
It omits keys/quotes/whitespace and can therefore only reject documents already
over the limit. The actual MarshalIndent byte length is the final admission
check, before metadata accounting, WriteFile or Rename. This bounds encoded
input/output bytes; decoded Go objects and JSON encoder buffers have their own
overhead, and it adds no new record-count or path/schema restriction.

Oversized legacy metadata remains preserved with an explicit error and requires
a separately reviewed migration. A pathological new record can still be
rejected even though its snapshots satisfy the separate capture/path limits.
The original metadata and retained record refs remain unchanged; existing
failed-admission rollback removes only the rejected record's new refs. As with
capture-limit failures, the turn's before/after recovery pins and retry owner
remain protected. No automatic cleanup, migration, recapture or mutator retry
is introduced by this metadata guard.
