# Worker recovery and continuation — 2026-09-25

## Trigger

The GunMayhem audit found a review worker that failed on a provider stream error after 42 model steps. The coordinator still had the worker in memory, but recovery guidance was present only for the special "max steps" failure. Investigation also reproduced two structural problems:

1. Generic tool-error formatting kept only the last 2 KiB of the task result. With a long partial report, this dropped the task header/status and initial findings. The coordinator could need a separate result retrieval merely to identify the interrupted worker.
2. A registered send_message tool was not necessarily active for invoke_tool. This produced a repair/discovery round when using the stable dispatcher. GUI rebuilt the parent registry between turns, losing even runtime activation despite retaining the worker and its conversation.

## Change

- Failed task/send_message results keep a bounded worker ID, role, status and error first, followed by a head/tail excerpt explicitly marked as an incomplete report.
- A failed worker with retained context includes a short continuation hint. Running, explicitly stopped, canceled, missing-loop and exhausted token-budget cases do not invite a retry.
- Tiny observations retain their existing historical-state label and inline budget. Full partial reports and larger observations remain retrievable from the ordinary output store.
- The error wrapper preserves the original cause for errors.Is/errors.As, cancellation and error classification.
- Creating a worker activates the existing send_message tool in the coordinator registry. This discovery state uses the normal session persistence mechanism. No tool is added to the child's restricted registry.
- A fresh GUI parent registry restores continuation availability when the engine still holds workers. A stable thin tool schema/catalog prefix remains byte-identical.

No system prompt, tool description/schema, provider transport, automatic retry or extra model call was added. The existing OpenCode Zen path is unchanged. Worker context still lives in memory: this is not worker recovery after an application restart. Existing worker retention/eviction rules still apply.

## Regressions and measured scope

Tests failed before the fixes and pass afterward:

- Long failed report: worker identity/status, both report endpoints, tiny historical evidence and a full-output retrieval handle reach the coordinator; UTF-8 and bounded size are checked.
- Simulated read followed by a provider stream failure: direct continuation in native and thin modes retains the original tool evidence. The fixture performs one file read total and creates one worker; resumption adds one worker model request, without rediscovery or another read.
- Two real GUI engine runs with a scripted provider: the second turn resumes worker-1 via invoke_tool. Before the fix it returned a dispatch refusal; afterward the worker completes and its question/progress use the current run.
- Canceled/stopped/busy workers and exhausted token budgets do not receive misleading retry guidance. Existing budget state is unchanged.
- Earlier success/evidence handoff tests still pass.

These are deterministic protocol regressions, not live-model latency benchmarks. They remove a reproducible failed dispatch and protect reusable work after an error. They do not establish a speedup for GunMayhem's 212-step initial review or solve overlapping task selection.

Validation: go test -timeout 90s ./... and go vet ./... passed. CLI/GUI builds and CLI --help were checked. Test logs, source backups and installed binary hashes are in .tmp/worker-recovery/.
