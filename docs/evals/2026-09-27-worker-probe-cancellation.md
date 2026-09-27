# Do not persist caller cancellation as a worker-backend failure

## Confirmed behavior

The shared task tool cached its optional first worker health probe with sync.Once. Cancellation of the owning task made that probe return context.Canceled, which permanently selected the coordinator model for subsequent tasks. The CLI/TUI wires this probe to an actual GET /v1/models request for eligible task_model backends. GUI currently leaves the optional probe unset.

A canceled task also executed repo preflight, created a child loop and registered an unused worker before runWorkerLoop noticed cancellation. Concurrent task calls waited uninterruptibly inside sync.Once while another probe was in progress.

These are code-backed regression cases. No occurrence count or latency saving is inferred from the historical session. Inspection also confirmed that send_message reuses the worker's Loop and common pruning configuration; it does not automatically reseed the entire parent history.

## Fix

- Reject an already-canceled task before preflight and child creation.
- Run backend selection under the actual child context and check cancellation again before setting up the child registry/loop.
- Cache completed health results; a caller-canceled attempt leaves the backend untested for the next requested delegation.
- Share a single in-flight probe through a completion channel. Waiting callers can cancel independently, and an uncanceled waiter may perform the next attempt after the owner cancels.
- Preserve real backend failure fallback, the five-second probe timeout and one warning per completed failure. No periodic probe, new generation call or startup request was introduced.
- Release in-flight waiters even when a custom probe panics and a caller recovers it.

The no-probe path returns the configured provider directly. GUI gains the shared early cancellation checks without acquiring a new network probe. Local/cloud tool profiles, special OpenCode Zen headers, normal task continuation and portable storage remain unchanged.

## Verification

The baseline overlay fails because a canceled task still prepares/registers a worker, a canceled probe is never retried and an in-flight waiter does not observe its cancellation. Corrected tests cover the next two tasks on the selected backend, actual waiting-caller cancellation, a healthy waiter retrying its canceled owner, genuine failure caching and panic cleanup.

An application-level httptest fixture exercises the production GET /v1/models wiring: cancel the first HTTP request, then execute two tasks. Both later reports identify the selected worker model; the server sees exactly two requests (canceled attempt plus cached success). Model generation uses Echo and makes no external calls.

Full go test ./..., go vet ./..., both executable builds and CLI --help smoke pass. Concurrency is exercised with channel handshakes; the Go race detector was not run because the configured build environment has CGO disabled.

Artifacts and backups: .tmp/worker-probe-cancel-2026-09-27/.

No live wall-clock or token-saving claim is made. This prevents unnecessary canceled-task setup and unintended long-lived model fallback.
