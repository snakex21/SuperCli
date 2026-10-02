# Fal video completion without polling

Date: 2026-10-02. Baseline: integration commit 24f0147.

The configured video adapter now submits exactly one job, then waits on one authenticated `GET <status_url>/stream` connection. After a complete `COMPLETED` SSE frame it closes that response and makes one result GET, followed by the bounded unauthenticated media download. It returns the ready local artifact in the same tool invocation; no additional model turn, background service, webhook or reconnect is introduced.

The official [fal queue contract](https://fal.ai/docs/documentation/model-apis/inference/queue) describes `text/event-stream` status objects on `/status/stream` with the connection held until completion. Verified from documentation on 2026-10-02; no generation or status API was called.

The existing local three-state fixture previously made three status GETs. It now proves one submission POST, one status stream, one result GET and one download (four requests total, previously six). This is a protocol request-count comparison, not a measured live latency, RAM, token or model-turn reduction.

The stream is context-bound and retains at most one status event. JSON data per event is limited to the existing 1 MiB metadata budget; the whole streamed input is capped at 16 MiB, including comments. LF/CRLF, comments and multiline data are supported. An unterminated event or EOF before completion, malformed/unknown status, mismatching request ID, HTTP/Content-Type failure or oversized stream is an honest error. There is no application retry, re-POST or polling fallback. Existing trusted best-effort cancellation remains one PUT before terminal completion, with the existing three-second cleanup deadline; completed failures do not cancel an already completed job.

Same-origin authenticated URL checks, escaped status path segments/query preservation, redirect refusal, credential-free allowlisted downloads, output byte limits and portable workspace output are unchanged. The stream is closed as soon as its terminal frame arrives, even if the server has not sent EOF.

Configuration/UI sources were intentionally not changed. Existing `poll_interval_milliseconds` fields and validation remain accepted for compatibility but do not control this adapter's stream. Ordinary chat still allocates no media HTTP client and performs no media requests when generation is disabled.

Focused offline coverage adds URL/escaping cases; framed status success; terminal-before-EOF cleanup; blank/comment/CRLF/multiline behavior; early EOF and malformed payload rejection; event/line/aggregate limits; context cancellation with no data; no reconnect/resubmission/result-fetch after stream failure; and no auth redirect/error-body leaks. Existing image, confirmation, queue cancellation, download/output and private-network tests remain included.

Validation:
- `go test ./internal/tools/mediagen`: PASS (0.801 s package test execution on final source).
- `go vet ./internal/tools/mediagen`: PASS.
- `git diff --check`: PASS; only repository LF-to-CRLF informational notices.

Owned files:
- `internal/tools/mediagen/providers.go`
- `internal/tools/mediagen/tool_test.go`
- `internal/tools/mediagen/providers_stream_test.go`
- This report.

No live API, provider/model, VM or user application was invoked. No config, UI, Zen, staged state or executable was changed by this subtask.
