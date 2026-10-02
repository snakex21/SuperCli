# Goal evaluation: SSE framing without a payload-sized formatting copy

Date: 2026-10-02. Baseline: main `0cc12b9`.

## Change and invariants

handleChat previously sent every event with `fmt.Fprintf(w, "data: %s\n\n", ev.marshal())`. The formatter copies the complete JSON into its printer buffer, even though the JSON is already serialized and escaped. Large payloads therefore allocated an additional output-sized buffer before HTTP writes.

The new private writeSSEFrame writes the fixed prefix, existing JSON bytes and fixed suffix directly. JSON marshaling, fallback error encoding, event metadata/order, coalescing, cancellation, durable writes and the existing one Flush per complete event are unchanged. Prefix/payload failure or short write stops the remaining writes for that frame. The handler still uses its existing disconnect/cancellation policy; no timer, additional flush, buffering delay, prompt, provider call, background task or dependency was added.

Production net/http HTTP/1 and HTTP/2 writers implement io.StringWriter, so fixed framing does not require per-string conversion there. The number of Write calls changes from one to three, within the existing HTTP buffer and before the existing Flush. The byte stream remains identical.

## Measurements

Windows amd64, Go 1.26.2, Ryzen 7 5800X3D, GOMAXPROCS=2. The first writer benchmark includes real wireEvent.marshal plus framing into discard/bufio sinks. It is a transport component measurement, not an end-to-end provider test. The buffered 128 KiB case allocated ~279 KB before /140 KB after; 1 MiB ~2.52 MB /1.07 MB. Small frames remove one allocation (24 bytes) without a reliable broad timing claim.

The final matched A-B-B-A loopback benchmark starts a real httptest HTTP server, sends first/message + tool result + done with Flush after each, and reads the complete response through a reused local connection. Requested benchtime100ms, two separate-process samples per version; medians below. Network/server/client allocations are included.

| Tool-result payload | Legacy time | Direct time | Legacy B/op | Direct B/op | Allocation counts legacy/direct |
| --- | ---: | ---: | ---: | ---: | --- |
|32 B|83.443 µs|79.221 µs|8,330.5|8,260.5|89/86|
|4 KiB|88.834 µs|91.058 µs|13,124|13,072|90/88|
|128 KiB|276.890 µs|254.014 µs|293,560.5|148,073|97/89|
|1 MiB|1.506 ms|1.429 ms|2,177,835.5|1,169,795.5|103–104/94|

The allocation reduction for large payloads is the principal benefit. Small response timings overlap and the 4 KiB median is slightly slower; no universal latency improvement is claimed. HTTP timing varies with the OS, allocator, GC and short runs. These are cumulative transient bytes, not measured peak/RSS or WebView2 RAM. Request tokens, model work and number of agent turns are unchanged. No live Qwen/Zen/GPU run was performed.

## Validation

- Exact legacy/direct frame equality for Unicode, HTML-sensitive characters, U+2028/U+2029, escaped newlines/quotes/backslashes, invalid UTF-8, NUL, reasoning counts, large tool output, terminal and worker metadata.
- Real loopback HTTP/1 and TLS HTTP/2 bodies equal the legacy byte stream; negotiated protocol is checked.
- A completion-channel barrier proves the first flushed event reaches each HTTP/1 and HTTP/2 client while the server cannot yet generate the terminal event; no progress polling or sleeps.
- Error and short-write injections at every framing stage stop later writes.
- Related Chat/Stream/HardProtocol/SSEFrame tests passed after integration; repository-wide verification follows in the combined report.

Tests/benchmarks: stream_frame_test.go, stream_frame_bench_test.go. Raw isolated and final loopback results are retained in `.tmp/sse-frame-audit-2026-10-02`, including loopback-abba.json. No user conversation, credential, window or provider request was needed.
