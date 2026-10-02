# Interrupted diagnostic output persistence

Date: 2026-10-03. Baseline: main `60f4e91`, dev12.

## Confirmed defect

The ordinary tool-result path attaches the loop's output persistence before retaining omitted diagnostic text. `cancelledToolResult` instead called `Registry.ModelResultContent`, which creates a background context without that binding. An interrupted tool could therefore return an opaque read_output handle backed only by the in-memory LRU. Restoring a conversation with a new registry, or restarting SuperCli, could no longer retrieve that omitted evidence.

Read-only aggregate inspection reused six previously selected portable sessions: **2,980 tool-result messages**. One saved `task` result in S14 contained `TOOL_OUTCOME_UNKNOWN`, a 708-byte visible result, and a handle advertising **61,933 bytes** of additional output as **in memory only**. The portable tool_outputs table contained **zero persistent copies** of that handle. S14 also had five TOOL_NOT_STARTED results; the other five sessions contained neither marker. Session IDs, handles, prompts, paths, reasoning and source payloads are absent from exported evidence.

The current baseline reproduced the lost archive for both a long Result.Text and an explicit Result.RetainedText: persistence received zero writes. This establishes an unavailable diagnostic handle after a fresh loop. It does **not** establish a historically repeated command, an extra model turn, a token saving or a RAM saving.

## Change and limits

The cancellation result attaches the existing `Loop.ToolOutputs` binding to its detached cleanup context and calls ModelResultContentContext. The same OutputStore retention rules and the existing **one-second SaveToolOutput timeout** apply. The tool is not retried. Cancellation remains TOOL_OUTCOME_UNKNOWN after dispatch and TOOL_NOT_STARTED before dispatch; the call/result identity, UI output and wrapped cancellation cause remain intact. Interrupted work still does not become a model failure or a successful verification result.

Only omitted output requiring retention triggers a persistence write. Small diagnostics and empty/not-started results have no output write. If persistence fails or reaches its deadline, the model still receives the truthful in-memory-only handle and current-loop retrieval works. Oversize results and missing persistence keep their existing OutputStore fallback. No new cache, service, dependency, provider request, instruction, tool contract, reasoning rewrite or OpenCode Zen change was added.

This is a recovery reliability fix. An interrupted large-output cleanup now may spend up to the existing one-second timeout attempting its archive write. Ordinary successful tool execution is unchanged; no additional model-processing latency reduction or process RSS reduction was measured or claimed.

## Verification

The isolated baseline/candidate overlay ran through the real agent invoke boundary without a model or child program. Baseline failed both persistence assertions; candidate passed. The durable regression uses the real portable SQLite session writer and a fresh independent registry/Loop:

- A synthetic **61,933-byte** Result.Text and a byte-identical explicit RetainedText both remain retrievable with fresh-loop read_output after interruption.
- Saved output equals the original bytes; output persistence adds no transcript messages.
- Persistence failure and a cooperative backend that actually waits for the one-second deadline preserve memory-only output and do not claim a durable archive.
- Small started and not-started cancellation results perform zero output writes and preserve the deadline/cancellation cause and outcome marker.
- Protocol IDs, UI result text, output handles and cancellation failure bookkeeping remain valid.

`go test ./internal/agent -run "Test(Cancelled|Cancellation|Interrupted|WorkerRetainsOutput)" -count=1` passed in **1.246 seconds**; `go vet ./internal/agent` passed. No live model, screenshot, hardware configuration change, stage, commit or executable build was performed by this worker.

Production ownership: `internal/agent/tool_cancellation.go`, `internal/agent/tool_cancellation_output_test.go`. Portable ignored evidence: `.tmp/goal-runtime-round10-2026-10-03/`, including aggregate metadata, the redacted interruption observation, baseline/candidate overlays and completed outputs, portable test runner and frozen hashes.
