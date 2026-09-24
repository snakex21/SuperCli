# Fewer discovery rounds and clearer tool failures — 2026-09-23

## What motivated the change

The recent USOS explore worker in session `02e9bba014852afe` used 19 model
steps, including 11 separate `list_dir` calls and 6 `read_many` calls.
Recorded total input was 156,161 tokens. This is evidence of expensive discovery,
not proof that all those reads were redundant. Several older failures in the
same history were already fixed in previous changes.

## Implementation

- `list_dir` accepts optional `depth: 1..4`. Omission keeps the existing
  one-level behavior and output. Deeper listings use relative paths and a
  breadth-first traversal, with one shared 500-entry default cap.
- Dependency/build/cache descendants use the existing search ignore set.
  Excluded names remain visible; explicitly selecting an excluded directory
  still works. Directory symlinks are not followed.
- Results distinguish a complete tree (with marked exclusions), a depth limit,
  and an entry-limit truncation. This gives the model evidence about coverage.
- Unknown-tool errors now identify available alternatives or tool discovery,
  instead of asking for another call with repaired JSON. Suggestions for a
  retired editor require the replacement editor to exist in that worker.
  Invalid JSON on a real tool retains its existing repair behavior.
- No new system instruction, helper inference, persistent index, provider
  branch, or runtime dependency was added. The serialized `list_dir` ToolDef
  shrank from **406 to 402 bytes**; this is a byte count, not a tokenizer claim.
  OpenCode Zen transport, placeholder tools, headers, and protocol are untouched.

## Live method

`scripts/exploration-economy/main.go` creates an isolated fixture under
`.tmp/exploration-economy/repo`: nine Go paths across five source directories,
plus generated/cache decoys. It calls the real `task` explore worker with the
same Polish request to enumerate Go paths under `src/` and `cmd/`.
Only read-only file/search tools are registered; no shell or edit tool runs.

A follow-up through real `send_message` asks the same worker for the two auth
paths. Tool executions, worker steps, provider-reported input/output tokens,
elapsed time and final answers are saved in JSON. Correctness checks require all
nine expected paths in the first report and both auth paths in the follow-up.

The before executable uses a Go overlay with the original `list_dir.go`.
The first pair ran before then after, the second after then before. Local Qwen
and cloud Muse run independently. Both use reasoning effort low; no models were
loaded/unloaded or reconfigured. The second pair adds explicit coverage labels;
the final Muse run additionally includes the unknown-tool error correction.

This is a small synthetic sample. Qwen chose search instead of the new directory
depth in both after runs, so its improvement cannot be attributed directly to
recursive listing. Server cache hits and prefill time were not measured.

## Results

Tool attempts include unavailable-tool calls. Those attempts are absent from
the successful-tool trace but present in worker statistics/history.

| Model/run | Variant | Model steps | Tool attempts | Input tokens | Output tokens | Discovery seconds |
|---|---|---:|---:|---:|---:|---:|
| Qwen A | before | 3 | 7 | 4,295 | 433 | 27.114 |
| Qwen A | after | 2 | 2 | 2,613 | 390 | 20.394 |
| Qwen B | before | 4 | 9 | 7,123 | 946 | 40.415 |
| Qwen B | after | 2 | 2 | 2,739 | 644 | 26.337 |
| Muse A | before | 5 | 9 | 10,504 | 1,274 | 13.009 |
| Muse A | after | 4 | 3 | 7,558 | 803 | 24.299 |
| Muse B | before | 5 | 9 | 10,202 | 1,080 | 9.610 |
| Muse B | after | 4 | 3 | 7,415 | 961 | 9.744 |
| Muse final | both fixes | 4 | 4 | 7,292 | 914 | 9.165 |

Every discovery report met the path checks. Muse used eight individual
listings before, versus one, two and three listings in the after/final runs.
Its first after run also searched again. The final run first listed the root,
then expanded `cmd` and `src`.

Muse attempted the existing Zen `bash` placeholder, mapped to unavailable
`ctx_execute` in this read-only worker. Execution was rejected. The final
error correctly listed available tools and recovery succeeded. There is no
evidence here that changing this error alone saved another model step.

All **nine follow-ups** were correct, used **one model step and zero tool calls**.
Thus existing worker continuation reused the discovered evidence. No new worker
memory mechanism was needed. Final Muse follow-up used 2,670 input tokens,
versus 3,263 and 3,088 in the before runs; a shorter discovery history also
reduces the context carried into a continuation.

The supported outcome is fewer discovery calls and less total input in this
fixture. Cloud wall time did not consistently improve, and this is not a
whole-application speedup or a promise for arbitrary tasks.

## Validation and incidental test repairs

New tests cover default parity, depth boundaries, explicit ignored roots,
one global entry limit, broad coverage before deeper expansion, relative roots,
cancellation, empty/file roots, and symlink exclusion (subject to host support).
Tool-error regressions cover available alternatives, discovery, empty/large
registries, unchanged arguments and restricted workers.

The first full suite exposed two pre-existing timing-sensitive tests:
- GUI question watchdog used 20 ms, too little scheduling headroom during
  concurrent builds. Its test timeout is now 200 ms and the simulated user still
  waits three times longer, preserving the pause check.
- Memory deduplication claimed to backdate entries but never assigned the dates.
  It sometimes expected the older duplicate to survive. The fixture now pins
  database timestamps and checks that the newest duplicate survives.

No production memory or watchdog behavior changed for these test repairs.
The original question test passed ten isolated runs; the deterministic memory
test passed ten runs. Final `go test ./...`, `go vet ./...`, and both release
builds passed.

Raw evidence, before-source snapshots, process results and check logs:
`.tmp/exploration-economy/`. Before/after experiments can be rebuilt with the
saved overlay and `./scripts/exploration-economy`; pass the exact model ID and
an absolute result JSON path. Do not run the fixture against user source files.
