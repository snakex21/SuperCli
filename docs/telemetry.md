# Low-overhead performance telemetry

WebGUI persists the agent loop's existing phase timers in the same
`session_turns` row already written for each completed answer. It does not
create a second event log, instrument individual stream deltas, retain prompts
or tool output, or make an extra model call. The hot path performs only
phase-level `time.Since` measurements. Aggregation and bottleneck diagnosis run
locally when the Usage panel is opened.

Recorded counters cover model/backend time, tool-batch time, pure CLI
preparation, overlapping session persistence, steps, helper/background model
calls, failed/canceled calls, tool failures, and bounded per-tool timing sums.
Legacy rows without phase data are excluded instead of becoming false
zero-duration samples.

The local/cloud live matrix is opt-in and skipped by ordinary tests:

```text
SUPERCLI_EVAL_LOCAL_URL / SUPERCLI_EVAL_LOCAL_MODEL
SUPERCLI_EVAL_CLOUD_URL / SUPERCLI_EVAL_CLOUD_MODEL / SUPERCLI_EVAL_CLOUD_KEY
go test ./internal/agent -run TestBackendMatrix_Live -v
```

It logs only JSON counters and timings. A production-path small-provider
navigator probe uses `SUPERCLI_LIVE_TASK_BASEURL` and
`SUPERCLI_LIVE_TASK_MODEL`.

Compaction intentionally waits for a complete summary because the next model
request cannot safely consume a partial one. Its cost remains visible as
`model:compact`. Worker activity already streams tool starts and completions
through the normal event channel.

Worker scratchpad retention is also bounded without a background watcher:
32 KiB per note, 32 notes and 512 KiB in total. Cleanup runs only on a
scratchpad write.
