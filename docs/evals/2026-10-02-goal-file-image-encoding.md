# File image data URI encoding with one output buffer

Baseline: 2b4c132 (dev11), Windows amd64, Ryzen 7 5800X3D, GOMAXPROCS=2. This round used CPU/differential fixtures only. No model request, real screenshot, image decoding, model reload or configuration change occurred.

## Finding and port

For a file-backed active image, resolveImageURL read the file, used base64 EncodeToString, then concatenated that string with the data URI prefix. EncodeToString owns an encoded byte buffer and copies it into a string; concatenation makes another encoded-size output. Those intermediate full-image allocations are unnecessary.

The file branch now grows one strings.Builder to the exact URI size and writes the existing standard base64 encoder into it. The encoder has a small fixed buffer; the builder returns its owned final string. There is no custom base64 implementation, unsafe code, dependency or cache.

URL and inline Data branches remain unchanged. File reading, wrapped read errors, precedence, media type values, validation and caller limits retain their prior behavior. Empty files or missing media types return the same incomplete-input error after the same file read. Active-image refs are not mutated.

The shared helper is used by the existing OpenAI-compatible, Responses and Anthropic builders. No provider routing/header/endpoint or special Zen source was changed.

## Observed sizes and measurement scope

A bounded metadata-only stat of the six file image references in two completed saved sessions found 22,718; 84,084; 348,258; 349,441; 496,670 and 699,020-byte files, all without inline Data. Pixels were not read for this metadata audit. These are preserved file sizes, not proof that every original file was transmitted: analysis can use a smaller derivative and dormant/default-preview images do not send pixels.

Synthetic binary fixtures used four of those observed sizes. They contain no user image or prompt. The benchmark measures the existing file-read/URI conversion alone and a five-message OpenAI request containing an image, a completed read-only tool pair and trailing context. This is request building/serialization, not the full GUI Loop, network, decoding, model prefill or generation.

Each exact-source overlay ran three 150-ms samples per case. File creation and initial setup are outside timing; the OS file cache is warm during these repeated reads. Medians:

| Input bytes / operation | Old allocated B/op | New allocated B/op | Old µs/op | New µs/op |
| --- | ---: | ---: | ---: | ---: |
| 22,718 / URI | 123,457 | 59,100 | 78.57 | 77.30 |
| 22,718 / request | 160,030 | 95,196 | 119.54 | 121.47 |
| 84,084 / URI | 434,781 | 206,570 | 194.17 | 135.86 |
| 84,084 / request | 562,490 | 334,932 | 345.88 | 263.78 |
| 348,258 / URI | 1,753,869 | 821,078 | 512.44 | 386.44 |
| 348,258 / request | 2,515,594 | 1,508,188 | 954.08 | 874.06 |
| 699,020 / URI | 3,507,054 | 1,640,299 | 798.53 | 695.74 |
| 699,020 / request | 4,589,740 | 3,113,825 | 1,787.07 | 1,539.41 |

URI allocation falls about 52–53% at these sizes. The 699,020-byte full request allocates about 1.48 MB less, approximately 32%. The larger cases measured lower CPU medians, but timing varies substantially with filesystem/GC/encoding-json pool behavior. The smallest request did not improve in CPU time. No universal latency or end-to-end speed claim follows.

Allocation traffic is not process RSS. The exact same pixels, base64, request bytes and tool/history content are sent, so there is no input-token, model-turn, native TPS or inference-speed change.

## Exact behavior and validation

The differential fixture compares the previous helper with the candidate over empty and short files, all byte values, base64 padding boundaries, encoder-buffer boundaries, four observed sizes, arbitrary/large media types, missing files, incomplete refs, nil images, URL/Data/file precedence and unchanged image refs.

Twelve full request bodies (four sizes × OpenAI/Responses/Anthropic) were byte-identical against the previous URI encoding: length and SHA256 match. This includes each builder's existing tool-pair repair, trailing-system placement and image representation.

Durable coverage is in internal/llm/image_file_encoding_test.go; BenchmarkFileImageEncoding reproduces the synthetic allocation fixture without private data or network access.

After porting:

    go test ./internal/llm -count=1
    go vet ./internal/llm

Both passed; the scoped suite completed in 4.597 s. No further benchmark/model call ran after this pass. Root owns full integration/builds.

Portable evidence is under .tmp/goal-runtime-round9-2026-10-02: exact baseline/candidate source overlays, run-image-audit.cmd, benchmark outputs, redacted file metadata, twelve body hashes and parsed measurements. Configured Go/temp caches remain inside the repository; module fetching is disabled.

## Unported lead

Anthropic request construction currently builds ordinary assistant blocks before a matching provider-native content block can replace them. The inspected saved sample contains native chat blocks, rather than evidence of material Anthropic native replay frequency. No change was made: skipping that work without accounting for existing argument errors could alter validation or signed/native replay semantics.
