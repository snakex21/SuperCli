# Deferred checkpoint changes after restart

## Reproduced problem

A real wrapped file mutation outlived the foreground response. Checkpoint
completion durably saved `a.txt`, but an injected SQLite UPDATE failure kept
`session_turns.file_changes_json` empty. After closing and reopening Engine,
the actual transcript loader still displayed the original counters and no
file changes. Baseline regression failed with this precise missing payload.

## Change

Only deferred completion obtains a key from its existing active-pin identity,
under Turn.mu, before starting the finalizer. The checkpoint Record and the
first foreground summary store that key and the initiating immutable user row.
An ordinary inline completion has no new key or background finalizer.

The summary binding has immutable ownership and a monotonic resolved flag.
Late completion and explicit transcript loading use an exact physical summary
row, the canonical binding and the original user receipt. SQL checks those
identities atomically. StoreGate serializes the update with cooperating rewind,
delete and retention operations. A conflicting UPSERT cannot replace ownership,
reset resolved state or overwrite already recovered FileChanges.

The real transcript/page loader uses summary rows it already read. Only pending
bindings request a fresh bounded checkpoint metadata read. Resolved and ordinary
summaries do not request that read. Recovery never starts a model, Git command,
retention pass, polling loop or autonomous retry. Busy stores are skipped.

Recovery applies to a retained, uniquely matching Record. An expired, missing,
legacy, malformed or foreign record is not inferred from current workspace
files, timestamps or reused message sequences. A prepared retention journal
blocks repair. A drained no-op resolves empty changes when its live completion
can persist successfully; a missing durable Record after a failed no-op write
remains unresolved. Existing counters and conversation content stay readable.

## Cost measurement

The same synthetic ordinary durable summary UPSERT was measured in three 500 ms
runs before and after, using the same schema. Baseline median: 551,159 ns/op,
about 3,200 B/op, 55 allocations. An initial dynamic SQL implementation added
about 3.4 KiB per write and was replaced before installation with constant SQL.
Final median: 555,192 ns/op, about 3,300 B/op, 56 allocations. The roughly 4 us
timing difference is within a small disk-sensitive comparison; it is not proof
of a meaningful latency improvement. The additional allocation is about 100 B
per normal summary write. Pending bindings add small durable identity metadata
only for deferred work. This is a reliability repair, not a measured TPS/RSS
improvement or proof of fewer turns on real model tasks.

## Verification

The restart regression now passes. Tests cover both event orders, a callback
before foreground binding, concurrent summary/record events, drained no-op,
failed capture, immutable ownership, same-owner UPSERT, rewind/reused sequences,
exact physical rows, missing assistant rows, duplicates, stale managers,
expired/legacy records, malformed metadata and a busy/prepared checkpoint store.
After successful repair the test makes checkpoint metadata malformed and confirms
that the resolved transcript still loads without requesting it.

A full runStream fixture delegates a real file mutation and persists the binding
on its first summary INSERT. Its saved transcript recovers the retained Record
through the real loader, preserving the original two coordinator and two worker
provider calls. This is synthetic API integration, not a live model quality test.

The source substitution proof reconstructs the previously validated source
`b13a77390fb0179ff98c6247e843ec34b260389984e236e3dc1559c56fc76df3` exactly.
Frontend source and special OpenCode Zen routing were unchanged. Test artifacts,
caches and application data remain inside portable application/workspace folders.

Fresh full package tests passed for checkpoint, session storage, agent, LLM,
LLM factory, app, WebGUI and TUI. The checkpoint package's complete fresh list
of 180 top-level cases was covered in four disjoint groups; platform/helper
skips keep their normal conditions. Vet passed for those eight packages.
Unrelated Go packages and unchanged frontend checks inherit the earlier
source-proven validation; they were not presented as freshly rerun here.

Validated source:
`4ab3759e3b2fa52bf15bd44911d7fb2066827060897d5fb1fad29b312322f44c`.
Detailed completed test logs, scope proof and benchmark receipt are under
`.tmp/optimization-oct8-2026/deferred-telemetry-design/`.

Ten outputs compiled from that exact validated source: TUI and GUI for Windows
amd64, Linux amd64/arm64 and macOS amd64/arm64. Both Windows EXEs were atomically
installed with recoverable backups; their `--help` startup smoke checks passed.
Linux/macOS outputs are compilation checks, not runtime validation on those OSes.
Installation receipt: `.tmp/optimization-oct8-2026/final-oct9-k.json`.
