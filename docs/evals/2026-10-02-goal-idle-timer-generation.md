# Expired TUI memory timer callbacks — 2026-10-02

Base: main 2b4c132 (dev11). The 15-second idle delay and memory summarization behavior are unchanged. The scheduler now tags each timer, invalidates that tag on Activity, replacement Schedule or Close, and rejects a stale callback before creating a job context. Job-generation ownership remains separate so a late canceled job cannot clear a newer job's cancellation function. A matching callback releases its consumed timer reference.

## Reproduction and verification

An ignored overlay pauses the real AfterFunc callback immediately before the scheduler mutex, using completion channels without polling. Timer.Stop returns false because the callback has already expired. The test then invokes Activity or replaces the schedule before releasing that callback. With the original scheduler, both cases still enter the production providerSummarizer through an actual Metered wrapper and make one stub provider Complete call. The candidate makes zero calls in each case. The ordinary expired callback still makes one call, and Close still prevents it. No network or native model inference occurs in this fixture; historical incidence and saved tokens are not measured.

Durable tests dispatch captured callback tags without sleeps and verify Activity, rescheduling, Close, replacement-timer preservation, normal context cleanup and a late canceled job overlapping a newer job. The overlap fixture waits for completion notifications and verifies that Activity can still cancel the new job after the old one finishes. An independent review checked both mutex orderings: Activity before fire rejects the stale tag; fire before Activity publishes its cancel under the mutex and then receives cancellation. The existing summarizer checks its context before Complete.

Full internal/app tests pass (1.239 s), and go vet ./internal/app passes. The controlled real-timer overlay passes its four cases; the original implementation fails Activity and replacement Schedule. There are no test hooks in production, no extra timer, no new prompt or tool schema and no provider-specific route. The special OpenCode Zen path is unchanged.

## Cost and limits

A matched A-B-B-A Schedule-plus-Activity fixture, GOMAXPROCS=2, Windows amd64 / Ryzen 7 5800X3D / Go 1.26.2, uses 100,000 iterations per sample and two samples per run. Original timings are 125.0–139.1 ns/op; candidate timings are 124.2–134.8 ns/op, so no CPU speedup is claimed. Both allocate twice per arm; allocated bytes rise from 128 to 136 B/op because the callback captures its generation. This small per-arm cost is accepted to prevent an unwanted model-backed helper from starting after activity, not presented as a RAM reduction. Earlier superseded benchmark receipts are not used for these figures.

This is a reproduced cancellation race, not a diagnosis of Qwen's earlier decode-rate decline or a measured live TTFT improvement. It does not cancel unrelated user applications, change model/KV/context settings or add a background service. All experiment, cache and temporary writes stay under the portable .tmp/goal-idle-timer-round9-2026-10-02 directory.
