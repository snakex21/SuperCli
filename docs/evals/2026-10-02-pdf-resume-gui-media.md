# Targeted PDF reads, session memory, stable GUI parsing and media previews

Measured on Windows, 2026-10-02. This round preserves previous work and the
OpenCode Zen provider path. No system instructions, extra classifier model,
new dependencies or background capture process were added.

## Changes and evidence

| Operation | Before | After | Scope |
| --- | ---: | ---: | --- |
| Resume decode live heap checkpoint | 34.60 MB | 18.11 MB | 64 native reasoning blocks of 256 KiB |
| Existing 8 MiB image externalization | 8.12 ms | 4.05 ms | Dedup path; one SHA calculation instead of two |
| Requested PDF page 17 extraction | 2.602 ms / 1.977 MB allocated | 1.007 ms / 0.196 MB allocated | Legal old pages 1–17 vs new page 17 only |
| PDF result sent to model | 3,364 B with retained handle | 2,061 B inline | Requested Balance value absent from old preview |
| Qwen PDF replay prompt | 958 tokens | 594 tokens | Same question, paired tool-call arguments and actual result |
| GUI closed 200k-character thought, 256 answer packets | 60.43 ms | 37.80 ms | CPU in actual schedule/paint functions with simulated DOM |
| GUI closed 1M-character thought, 256 answer packets | 231.10 ms | 115.68 ms | Same complete output and 257 paints |

These are operation-specific measurements, not whole-task latency or total
WebView2 RSS guarantees. Final live heap after resume was approximately
17.83 MB in both versions; the change removes temporary overlap during decoding.
The large native-resume CPU control was 57.62 → 59.06 ms in three short trials,
so row clearing is a memory improvement without a confirmed CPU improvement.
The GUI open-thought control had one 53.62 → 59.06 ms sample; an earlier control
was 46.05 → 45.38 ms. Closed-prefix reuse does not improve open reasoning.
Plain GUI text was 5.98 → 5.78 ms, with packet medians effectively unchanged.

### Targeted PDF context

read_pdf now accepts a 1-based start_page. max_pages counts returned pages;
the default full-read behavior and existing configured page cap remain intact.
Ranges, EOF and output-byte caps are explicit and checked without overflow.
Cancellation is checked between pages and after extraction of the current page;
the underlying GetPlainText operation cannot be interrupted halfway through a page.

The source comparison found the same useful principle in neighboring harnesses:
explicit offset/limit and truthful continuation instead of reading preceding
content just to reach a later item. Relevant primary local source:

- DeepSeek: packages/fs/tool-fs/src/read.ts, lines 55–60 and 150.
- Pi: packages/coding-agent/src/core/tools/read.ts, lines 16–17 and 147–178.

No neighboring code was copied. SuperCli already has retained output and paged
text reads; this change closes the analogous gap in PDF access.

Four sequential local Qwen 3.8 27B requests used AB/BA order, temperature 0,
seed 2718, reasoning_effort none and an 80-token response cap. Both new-result
replays answered the correct value, 2329. Both old-preview replays emitted
text requesting read_output and reached the response cap. Those requests were
not executed. The test demonstrates direct visibility of the requested fact
and a 38.0% reduction for this replay input, not a measured whole-agent turn
reduction. Full tool schemas were not included in this replay; the new PDF
definition is 53 bytes larger and remains discoverable.

### Session memory and image digest

ReadModelContext clears each local encoded row only after successful conversion
and append. Message content, native blocks, image references, tool pairs, error
ordering and the complete output digest remain identical.

ExternalizeImage reuses the full SHA computed during storage to derive its short
image ID. Filename, short ID, MIME, image bytes and legacy migration remain
identical. The image benchmark excludes physical file writing.

### GUI parsing

Assistant text uses a checkpoint only at a balanced, closed protocol-tag
boundary. Known append operations reuse that immutable prefix; nested, split,
orphan and repeated tags still pass through the existing suffix parser.
Replacement, recovery, disconnect and terminal boundaries invalidate the cache.
No new timer or first-answer buffer was added.

## On-demand screenshots and local media

send_screenshot retains clipboard as its default source and adds source:screen.
attach:false saves and presents the screenshot without putting pixels into a
model request. A capture returns small JSON metadata with a local path and a
stable preview identifier. Legacy ignored model arguments still work through
argument repair; other invalid arguments remain validated.

Windows screen capture uses native GDI, a bounded top-down bitmap and PNG
encoding; it starts no helper process. A native smoke captured 3840×2160 into
memory in approximately 0.22 seconds, with 482,251 encoded bytes. Desktop pixels
were not saved or published by that smoke.
The implementation follows the documented lifetime and synchronization contract
for [CreateDIBSection](https://learn.microsoft.com/en-us/windows/win32/api/wingdi/nf-wingdi-createdibsection),
[BitBlt](https://learn.microsoft.com/en-us/windows/win32/api/wingdi/nf-wingdi-bitblt)
and [thread DPI awareness](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-setthreaddpiawarenesscontext).

macOS uses installed screencapture; Linux uses installed grim or
gnome-screenshot. Nothing is downloaded or installed. Helper capture is bounded
and cancellable. The macOS clipboard bridge now decodes the source-form PNG
descriptor without reversed nibbles. Linux preserves installed-helper errors
instead of incorrectly recommending installation. Runtime desktop capture on
macOS/Linux has not been verified here.

Snapshots are saved below the supplied portable application data directory.
No profile or OS temporary-folder fallback is used. Exclusive file creation
avoids concurrent worker collisions. PNG signatures and declared MIME must
agree; empty or falsely labeled captures cannot produce a saved success.
The snapshot preview namespace remains within its exact root, including after
moving the application folder; it cannot expose unrelated configuration files.

show_media exposes a local image, MP4/WebM video or MP3/WAV/Ogg audio file using
only metadata. It checks a regular-file size of at most 32 MiB and reads a
512-byte MIME header, without adding media bytes to model history. A metadata
benchmark for an 8 MiB file allocated approximately 13 KB per operation.

GUI previews are lazy in both live tool results and resumed history. MIME,
rather than extension alone, selects the viewer, including valid files named
output.bin. Audio/video creates its player only after a click, with controls,
preload:none and no autoplay. Closing or switching the preview releases its
source. HTTP Range responses stream media without loading the entire file.
TUI shares both tools and returns the saved/local file path; inline video or
raster rendering in a terminal is not implemented.

Image/video generation APIs are outside this selected first phase. The current
chat-provider endpoint is not assumed to support generation.

## Validation and local delivery

- All Go packages: go test ./... -count=1, PASS.
- GUI: 19 test files, 118/118 PASS, including parser parity, first paint,
  lazy live/history media, MIME mismatches, cancellation and portable references.
- Native Windows capture and --help smoke for both executables: PASS.
- Strict snapshot namespace, folder relocation and video Range response: PASS.
- Builds: Windows amd64, Linux amd64, macOS arm64, GUI and TUI, CGO disabled.
  Cross-compilation is not runtime UI verification on macOS/Linux.
- Source hashes stayed unchanged during the six builds; copied Windows EXE
  hashes match the staged builds. Previous EXEs were backed up first.

Installed Windows binaries:

| Binary | Bytes | Increase this round |
| --- | ---: | ---: |
| supercli.exe | 26,834,944 | 29,696 |
| supercli-web.exe | 22,513,152 | 84,992 |

Detailed requests, responses, baseline fixtures, benchmarks, validation and
binary manifests are retained locally in
.tmp/efficiency-2026-10-02-round4. GUI parser measurements are also in
.tmp/gui-stable-thought-2026-10-02; session heap/digest measurements are in
.tmp/session-resume-memory-audit-2026-10-02 and
.tmp/session-image-sha-audit-2026-10-02.
