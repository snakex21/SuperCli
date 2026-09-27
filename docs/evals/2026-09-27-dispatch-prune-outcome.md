# Keep outcomes of late-dispatched tools during pruning

Date: 2026-09-27

## Confirmed failure

The agent can discover a tool and invoke it in the same assistant batch. The initial invoke_tool rewrite correctly defers an inactive target until tool_search executes. That late path preserves the original invoke_tool call/result names in the transcript for protocol pairing.

Budget-driven pruning previously recognized command, process and worker metadata only by the result's direct name. It therefore replaced such a late-dispatched process result with a generic marker and output handle, dropping the process ID, status and exit code. This was reproduced with the actual process manager and a child test executable, for successful and failing processes through both native calls and the thin sentinel dispatcher. It is a current-code reproduction, not a claim that this exact failure appears in the frozen GunMayhem trace.

## Change

For invoke_tool results only, pruning finds the matching call in the immediately preceding assistant batch and reads its exact tool field. A copied message supplies the known target name to the existing generated-result parsers. Actual history names, call arguments and call/result IDs stay unchanged. This works from transcript data after reload without depending on current tool activation.

Matching stops at a new user instruction or assistant batch; missing, empty, duplicate and mismatched IDs remain unknown. Target field lookup follows the dispatcher's case-sensitive key semantics. Only tools with existing special pruning behavior are recognized. File contents and refused-dispatch prose cannot supply process outcomes. apply_skill guidance remains protected through late dispatch as well.

Candidate markers are now prepared once and reused for both the reclaim estimate and final replacement. The previous path parsed/formatted each selected result three times.

No extra model call, instruction, tool execution, model-specific route, or process polling is introduced. The existing OpenCode Zen path is unchanged.

## Verification

- Four native/thin success/failure real-process cases fail before and pass after the change. Each starts one child process, performs one blocking wait and one discovery call.
- Completed results of 4,276 bytes and failed results of 2,273 bytes become 137/139-byte markers preserving process ID, status, exit code and an accessible full-output handle.
- JSON transcript roundtrip and provider-facing message projection retain the outcome and original protocol pairing.
- Pair-boundary, ID ambiguity, exact target-key, malformed envelope, file-prose and refused-dispatch tests pass. Internal image wrappers do not incorrectly break the batch.
- Existing command/process/worker pruning, current-step protection, refusal memoization and reclaimed-token accounting tests pass.
- Full go test -timeout=90s ./..., go vet ./..., CLI/GUI builds and CLI --help pass.

## Local benchmark and tradeoff

Three 200 ms samples, each pruning 64 process results plus a protected tail:

| Results | Before | Final | Allocated bytes/op, before -> final |
| --- | --- | --- | --- |
| Direct process calls | 3.10–3.22 ms | 1.04–1.14 ms | about 909 KB -> 318 KB |
| Late invoke_tool calls | 0.111–0.121 ms | 1.157–1.202 ms | about 34 KB -> 379 KB |

The late-dispatch path now performs metadata extraction that was previously missing. Its extra local cost preserves useful evidence; it is not a speedup for that path. Direct calls benefit from removing duplicate parsing. Pruning remains budget-triggered, not a new per-request operation below the threshold. No end-to-end provider latency or observed saved-model-turn claim is made.

Artifacts: .tmp/dispatch-prune-outcome-2026-09-27/ contains red/green results, a baseline Go overlay, benchmark-before.json, benchmark-final.json, complete suite/vet/build output and installed hashes. The earlier benchmark-after.json predates the final exact-key lookup guard; final measurements use benchmark-final.json.
