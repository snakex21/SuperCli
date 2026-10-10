# Transcript JSON streaming experiment: not adopted

The GUI already requests transcript pages of 60 messages, with a server cap of
500. The model receives a separate history projection. This experiment tested
whether encoding a response one message at a time would use less memory than
the existing whole-response JSON encoding. Production code was not changed.

A read-only aggregate of the main session database contained 18,931 messages
across 200 nonempty sessions. Its largest arbitrary 60-message window had
485,082 bytes of escaped visible content and tool arguments; the largest latest
60-message window had 361,447 bytes. The median latest window had 3,197 bytes.
The main database snapshot excluded a 9,331,832-byte WAL, so these are archived
distribution estimates, not an assertion about the newest live messages. No
private message contents, image pixels or credentials were exported.

The ignored experiment uses the actual response DTOs and checks exact JSON
bytes, including Unicode, invalid UTF-8, HTML escaping, empty/nil fields, images,
tool arguments, change records and pagination cursors. Writer errors and short
writes are tested. Both parity/error tests and the benchmarks passed centrally.

Five synthetic profiles were measured for one second with three samples each,
using a 4 KiB buffered output writer. Medians:

| Profile | Existing time | Streaming time | Existing bytes/op | Streaming bytes/op |
| --- | ---: | ---: | ---: | ---: |
| Ordinary 60 messages | 27.3 us | 31.6 us | 49 | 40 |
| Heavy 60 messages | 564.7 us | 561.5 us | 582 | 59 |
| Server limit, 500 messages | 4.575 ms | 4.695 ms | 33,018 | 210 |
| Large tool outputs, 60 messages | 7.537 ms | 7.316 ms | 105,720 | 3,854 |
| Legacy full response, 600 messages | 5.364 ms | 5.581 ms | 76,387 | 225 |

The ordinary response became about 16% slower and required 126 serializer
writes instead of one. This benchmark excludes real HTTP write/network costs.
Allocations/op were two rather than one for the streaming candidate.

Separate fresh-process heap probes confirmed that cold whole-response encoding
can retain a larger scratch buffer in Go's shared JSON pool. After a heavy-page
response, small health responses and an explicit GC, the isolated retained heap
was 524,600 bytes with whole-response encoding versus 8,504 bytes when streaming.
For the ordinary page it was 24,888 versus 1,000 bytes. Artificial maximum/legacy
profiles saved several MiB, but the GUI does not normally request those profiles.

The same pool also serves provider requests and other endpoints. A control that
preseeded it with a 1 MiB buffer retained that buffer with either implementation;
two idle GCs later released it with either implementation. A transcript-only
change would not fix pooled buffers donated by those other operations.

Decision: do not replace the ordinary serializer in this batch. The normal-page
CPU regression, extra writes and shared-pool control outweigh the modest
observed-page heap saving. This is not a measurement of WebView2 RAM or process
RSS. A future selective large-response change would need real HTTP benchmarks
and an evidence-based threshold before adoption. The fixture, source guards,
aggregate data and medians remain under
`.tmp/optimization-oct8-2026/transcript-json-experiment/` for reproducibility.
