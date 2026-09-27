# Worker identity survives context pruning — 2026-09-27

## Evidence

The fixed GunMayhem snapshot contains task reports at sequences 887, 949 and 1371 plus a continuation result at 889. Their generated headers identify worker-1/worker-2/worker-3 and the observed run state. Budget-driven pruning previously kept only the tool name and any retained-output reference, dropping the worker identifier. The original task arguments identify the role and assignment, not the assigned worker ID.

The same loss is reproduced with actual task/send_message machinery and scripted providers: after pruning a completed/failed report, the parent no longer receives the ID needed to continue that still-retained worker. This does not establish that pruning occurred at these four points in the original session or that it caused that session's duration; the frozen reports are an exact format replay.

## Change

The common pruning path now preserves `worker_id` and `worker_status` from the generated task/send_message framing. It handles ordinary notifications, the existing large-output wrapper, current failure handoffs and the known interrupted-call wrapper in the saved session. It inspects at most 768 bytes at the start, validates canonical IDs/states, and never searches arbitrary report prose for a plausible identifier.

The existing output handle remains available. No registry lookup, new disk access, model invocation or instruction is added. No worker is automatically resumed or restarted. Small reports that do not benefit from pruning remain intact. The original UI/persisted transcript, retention limits, token caps and cancellation behavior stay unchanged.

`worker_status=done` means that worker run ended normally, not that the requested implementation is complete or verified. In particular, report 887 explicitly said the work was incomplete. The marker makes no verification claim and no promise that a historical worker remains retained/resumable. Full report details still require the existing retained handle when omitted. This change applies to budget-driven tool-result pruning; separate completed-turn history omission is unchanged.

## Results

| Saved sequence | Model result bytes | Marker bytes | Retained metadata |
| --- | ---: | ---: | --- |
| 887 | 2,224 | 129 | worker-1, done, original output handle |
| 889 | 2,960 | 137 | worker-1, done, original output handle |
| 949 | 4,298 | 129 | worker-2, done, original output handle |
| 1371 | 708 | 131 | worker-3, failed, original output handle |

Four native/thin, success/failure integration cases use the ID recovered from the provider-facing pruned marker to call send_message. Each retains one worker and one original source-read tool execution, and the continuation's captured request contains the earlier evidence. These are deterministic scripted-provider checks, not a measurement of live-model turn savings.

Additional format cases cover plain, large and structured reports; done, failed, stopped and running workers; task and send_message; malformed IDs/states and unrelated report/file text. The previous real-process pruning regression also remains green.

## Validation and delivery

- Focused red/green tests and the four saved report replays: passed after the change, failed before it.
- `go test -timeout=90s ./...`: passed.
- `go vet ./...`: passed.
- CLI/GUI builds and CLI `--help`: passed.
- `git diff --check`: passed.

Artifacts are under `.tmp/worker-prune-identity-2026-09-27/`, including the local frozen report subset, checks, builds and installation/backup manifest. No live provider request or OpenCode Zen transport change was needed. No wall-clock speedup is claimed.
