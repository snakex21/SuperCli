# Engine memory Store identity — 2026-10-08

Engine cached project memory by workspace home. Relocating a project preserves
its registered storage key, so visiting the original and relocated homes opened
separate memory Stores for the same portable `memory.db`. Each Store owned a SQL
connection pool and an embedding worker; both remained until Engine.Close.

The change shares one Store per canonical backing database path. Observed homes
retain their original binding, allowing keepers created for running workers to
continue using the same database after relocation or project removal. Metadata
resolution and filesystem canonicalization run only on a home cache miss; normal
lookups retain the existing cleaned-home map lookup.

## Source behavior

- `internal/webgui/feat_memory_runtime.go` keeps home bindings and indexes owned
  Stores by the absolute, cleaned backing file path, resolving symlinks when
  possible and folding case on Windows. Windows spelling aliases of an observed
  home borrow its bound identity, including authoritative relocated keys that
  would be absent under the alternate spelling in case-sensitive JSON keys.
- A new home first resolves ProjectStorageKey to reuse an existing backing Store.
  When a Store must be opened, publication derives identity again from its actual
  Root. A concurrent metadata change between prediction and OpenProjectStore
  cannot publish that handle under the earlier, different database key. Only an
  unpublished redundant handle is closed; live Stores and workers are untouched.
- The original OpenProjectStore path still handles the first unregistered home,
  including persistent path-to-key registration. Cache reuse does not write the
  projects map or recreate a removed registration.
- Engine.Close clears both indexes and closes the backing-identity ownership map,
  so every shared project Store is closed once. Global memory ownership is
  unchanged. No cache size, SQL pool policy, goal Service or eviction policy changes.

The map is still intentionally persistent for the Engine lifetime. Sharing
aliases removes duplicate SQL pools; it does not impose a limit on distinct
projects or evict resources borrowed by live tools.

## Central comparison

The ignored fixture is
`.tmp/optimization-oct8-2026/store-identity-experiment/`. A Go overlay injects its
tests/benchmarks into the real WebGUI package. It constructs a minimal Engine,
creates only portable synthetic data under that experiment, and performs no
application/model/HTTP run. Source snapshots were saved under its `baseline/`
directory before editing Engine or the memory runtime.

The central pre-change runs passed after fixing the fixture's explicit dataDir
creation. The identical post-change fixture passed three times; benchmarks ran
three times with a 300 ms benchmark duration. Results were supplied by the central
runner from `go-store-identity-before-fixed-{0,1}.log` and
`go-store-identity-after-{0,1}.log`:

| Measurement | Baseline | After |
| --- | ---: | ---: |
| Nine observed homes of one relocated memory DB | 9 Stores, 9 embedding workers | 1 Store, 1 embedding worker |
| Project memory pools after a six-connection burst per Store | 9 pools, 18 open/idle | 1 pool, 2 open/idle |
| Summed native SQLite page-cache bytes after that burst | 9,252,044 B | 2,539,116 B median |
| Fully used Engine: memory, global, session, goal and credit pools | 13 pools, 22 open/idle | 5 pools, 6 open/idle |
| Fully used Engine native page-cache bytes | 9,590,594 B | 2,877,666 B median |
| Normal cached-home lookup, median | 198.1 ns/op | 187.6 ns/op |
| Normal cached-home lookup allocations | 0 B/op, 0 allocs/op | 0 B/op, 0 allocs/op |
| First lookup of eight mapped aliases, median | 155.227250 ms/op | 19.116639 ms/op |
| First lookup of eight mapped aliases allocated bytes | 171,294,304 B/op | 21,583,960 B/op |
| First lookup of eight mapped aliases allocation count | 36,876 allocs/op | 6,134 allocs/op |
| First lookup of eight mapped aliases Store count | 8 | 1 |

The three post-burst project cache samples were 2,521,708, 2,539,116 and
2,539,116 B. The measured native page-cache reduction was 6,712,928 B, about
6.4 MiB, in this synthetic alias fixture. This is not an RSS or total-RAM result.
The unregistered Windows-case fixture retained two spelling aliases and one
Store. Every observed pool had zero open/in-use connections after Close, which
also awaited each embedding worker's embedDone.

The alias-first-open benchmark excludes Engine.Close and uses a preseeded DB. It
measures repeated opening/migration/reconciliation work over one actual database,
not cold-machine startup or end-to-end agent CPU. The normal cache-hit benchmark
is separate so the optimization must not add metadata I/O or allocations to the
ordinary repeated-home path.

The identical isolated fixture and full central verification passed: 71 Go
packages, `go vet ./...` and 215 UI tests. Both applications compiled for all five
existing targets; Windows EXEs were installed with source/binary hash guards.
No Go tests or builds were executed by the editing agent.

## Regression and measurement coverage

Permanent WebGUI tests cover real projectAction relocation with captured old
keepers, removal of an already relocated project, unregistered memory persistence,
clean paths, authoritative Windows case aliases, concurrent mapped-home writes,
concurrent project-map edits while known keepers retain their bindings, distinct
backing database isolation, Engine.Close, and symlinked backing directories where
the host permits symlink creation. No production debug pointer or test-only hook
was introduced.

The isolated fixture relocates one project eight times and writes concurrently
through six previously captured homes. It seeds approximately 2 MiB of synthetic
text in 128 entries below the per-entry content limit. It measures distinct Store
pointers, embedding worker stack counts and actual DB.Stats. Native SQLite
`db_status64` samples CACHE_USED, SCHEMA_USED and STMT_USED on every existing idle
connection, reserved together so each is counted once. All operations finish
before those snapshots; no progress polling is used.

Its shrink_memory phase runs exclusively on synthetic fixture databases to
distinguish reclaimable page cache from the other native SQL structures. It is
not an instruction or implementation to trim production caches. Explicit Close
must leave every observed SQL pool with zero open/in-use connections.

## Limits and existing metadata semantics

Native CACHE_USED approximates SQLite pager cache. Those counters are not total
allocator commitment, Go retained heap, RSS, Windows working set or WebView RAM.
The bundled Windows SQLite source disables global MEMSTATUS by default, so a
zero sqlite3_memory_used would not prove zero native memory. The fixture uses
per-connection counters instead. The baseline is synthetic and does not promise
a fixed RAM reduction for every real session or explain disk-storage growth.

Engine's goal Services still share one goalDB. Credit storage owns a second pool
over the same supercli.db file; sessions and global memory each have one pool.
Under unchanged SQL defaults a pool retains at most two idle connections, while
temporary active connection bursts are uncapped. The issue addressed here is
duplicate pool ownership for the same project DB, not unlimited completed-worker
connections inside a single pool.

LoadProjectsMap/SaveProjectsMap still use independent ReadFile/WriteFile calls;
external simultaneous cold registrations or truncated metadata writes are not
made transactional by this cache change. Already observed keepers do not reread
or overwrite that mapping. A fresh Engine observes a subsequently edited valid
mapping, while an existing keeper keeps its previously observed identity.

Project removal still drops the path-to-key registration and preserves memory.db
on disk. Removing a previously relocated project and later re-registering its
new path can select the path-derived key after restart, because the original
relocation key has been removed from metadata. That existing remove/add behavior
is outside this change; no memory history or project metadata was rewritten to
alter it.
