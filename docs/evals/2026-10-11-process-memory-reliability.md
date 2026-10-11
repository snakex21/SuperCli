# Process output, readiness, memory and external checkpoints

The common CLI/GUI tool path now decodes child text before bounded capture.
UTF-8 (including split input chunks) is preserved; UTF-16 BOMs are supported;
invalid UTF-8 on Windows falls back to the system OEM code page, then ANSI if
OEM is unavailable. This fixes hidden Windows PowerShell's CP852 grouping
space and Polish characters without changing the locale, argv or command.
Automatic detection cannot distinguish every arbitrary encoding. Truncation
operates on decoded UTF-8, not on undecoded OEM bytes. Persistent process
snapshots also leave incomplete trailing UTF-8 characters for the next read.

`process_session` exposes `wait_ready`, with a required nonempty `ready_pattern`
regex in either retained stdout or stderr. Writes and process completion wake
the waiter via channels. `ready_timeout_ms` cancels only that wait. Cancelling a
wait preserves the process and unread output; `stop` controls process lifetime.
An optional `ready_port` makes one connection to 127.0.0.1 after the log match.
It never repeatedly probes a port; it does not support a port-only readiness
wait. A matching log from an exited process cannot establish readiness.

Snapshots distinguish a launch from observed readiness, include the observation
time, invalidate readiness on exit/failed port verification, and report the exit
code with native execution duration. Large-output previews retain the same
metadata. Readiness is historical evidence, not continuous health monitoring.
Detached shell launch success proves only launcher success; start the actual
executable through the persistent process tool.

Compaction preserves the latest paired process result, including failed exits
and readiness observation time, outside the model-generated summary. These
bounded observations survive pruning and subsequent compactions, and are
explicitly historical. The common prompt also separates launch, verification,
and current health. No model-specific rules or thinking-level changes were made.

Memory briefing filters duplicate user facts, automatic conversational filler
and qualified `NOTHING` notes without deleting original records. Automatic
extraction rejects conversational preference fragments; long explicit durable
requirements remain eligible. Unicode tokenization preserves accented facts,
and opposite preferences are not deduplicated together. No extra model call is
used for this filtering.

With full filesystem access, named external file mutations receive a scoped
checkpoint in the configured application data directory. A separate ordinary
manager covers each external parent directory; before capture must succeed
before the original tool runs. Existing retention, leases and conflict-safe
undo/redo are reused. Explicit directory operations retain the existing size,
file-count and symlink checks. External records are separate from the active
workspace's UI undo history; a fixed notice describes that distinction. Shell
commands still use their existing workspace checkpoint semantics. Sandbox-off
access and sensitive system roots remain restricted.

Validation logs and source receipts are in
`.tmp/process-memory-reliability-2026-10-11/`. Tests use the shorter portable
`.tmp/pmr-temp/` root because an existing Git ref-inspection test does not enable
Git's long-path option. This avoids a test-only broken-ref warning at MAX_PATH;
production checkpoint commands already enable long paths.

Validation completed: 11 relevant packages passed (full regression plus the
final changed-package rerun), `go vet -p 1` passed, and `go build -p 1 ./...`
passed. The hidden PowerShell regression also failed as expected with an
overlay of the original runner, then passed with the decoder. No live source
was reverted for that baseline check. The UTF-8 decoding microbenchmark
reported 1234.50 MB/s, 0 B/op and 0 allocs/op; this is decoder throughput, not
a claim about model or whole-agent speed under VM load. CLI and GUI binaries
were built, smoke-tested and atomically replaced with verified backups.
