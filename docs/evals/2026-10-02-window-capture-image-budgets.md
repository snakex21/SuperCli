# Targeted screenshots and bounded image analysis

The prior desktop capture photographed the visible desktop; a foreground SuperCli window could cover the intended target. It also defaulted to forwarding captured pixels to the model, even for a request that only asked to display a screenshot.

## Changes

- Windows window selection uses an exact title first, then a unique title substring, or an explicit window ID. Ambiguous titles fail with a bounded list of choices. No foreground activation, restore or desktop fallback is performed.
- Target-window operations run on demand in a hidden copy of the current executable. The parent waits for completion with a deadline; cancellation terminates this disposable capture process rather than leaking a blocked PrintWindow thread. No permanent capture process or profile data is added. Native tests found a 150% Windows DPI mismatch: matching the capture thread to the selected window's DPI context avoids incomplete/oversized frames. A synthetic covered-window capture preserved its red/blue content and left the foreground window unchanged. Single-helper launch measured a 25.1-ms median versus 49.2 ms for separate discovery and capture (five pairs, this fixture only).
- Desktop/window captures default to metadata and a chat preview. Explicit attach:true requests image analysis; clipboard retains its previous attach default. The tool contract states that automatic chat preview does not require model pixels.
- Analysis images default to a 1280-pixel long edge and a 1,048,576-pixel budget. The saved screenshot remains original. read_image accepts original-coordinate crops and image_detail:original for precise reading. Analysis metadata records original and transmitted geometry.
- File image reads use one opened descriptor and a bounded read, including when a file grows after its size check.
- Chat and composer image previews use bounded thumbnails. Original files are fetched for the enlarged preview. Derivatives remain app-local with a 128-file/64-MiB limit; only one source is decoded at once and no decoded raster cache is retained. Closing a modal clears image, iframe and media sources.

## Evidence and limits

A pre-change local Qwen test with a synthetic 3840x2160 grid demonstrated the avoidable cost. A direct metadata follow-up used 552 reported input tokens and 1.32 seconds; forwarding the same pixels used 4,636 input tokens and 13.61 seconds. This single-trial comparison isolates the follow-up transport, not native capture or tool choice. The desktop tool-selection arm reported 5,735 input tokens on its image-bearing follow-up, matching the user-supplied LM Studio log. No real desktop or clipboard pixels were sent to Qwen.

Default wording alone was insufficient in those initial live arms: Qwen explicitly chose attach:true, and the named-window arm listed windows before selecting a window ID. Those results are retained rather than presented as a successful default-selection improvement. The final contract is clearer, and actual attached pixels are now bounded independently of that choice. One final live transport trial using the same synthetic grid and model settings, with the bounded 1280x720 image, reported 1,535 input tokens, 13,107 request bytes and 3.555 seconds. The prior full-image arm reported 4,636 tokens, 43,975 bytes and 13.610 seconds. The original 32,433-byte PNG was unchanged; analysis PNG was 9,139 bytes. This is about 67% less total input and 74% lower wall time in this single trial, including the new CPU resize cost. Lower image dimensions do not guarantee identical token counts across model/projector configurations.

CPU-only 4K PNG preparation measured 225.6 ms and 38.3 MB total allocations for a 1280x720 analysis frame. This includes decoding the original and creating a bounded target; it is not a retained-RAM measurement. Exact original and cropped pixels, alpha edges, thin strokes, cancellation and decoded/encoded limits are tested.

For a 3840x2160 image, a 780x438 transcript thumbnail has approximately 1.30 MiB RGBA storage versus 31.64 MiB for the source (about 96% less per decoded preview). Actual WebView2 process RSS has not been measured. A synthetic first-thumbnail benchmark measured 57.7 ms and 34.2 MiB allocations, while the cached derivative measured 1.15 ms and 31 KiB. These are CPU microbenchmarks, not end-to-end GUI latency guarantees.

The full-resolution modal and WebP fallback retain their existing behavior. PNG/JPEG/GIF thumbnails and analysis are bounded; WebP analysis currently retains its encoded-capped original because no decoder dependency was added. Cropping unsupported formats returns an explicit error.

Minimized-window capture depends on the owning application continuing to render. Unsupported, blank or incomplete content is reported as a failure rather than returned as a successful screenshot. Linux/macOS desktop capture remains available; targeted background-window capture in this change is Windows-only.

The LM Studio find_slot warning comes from the llama.cpp engine. Upstream describes image-position warnings for hybrid/recurrent Qwen models: https://github.com/ggml-org/llama.cpp/issues/28166 . Reducing pixels can reduce image work; it does not repair the installed backend or promise to remove its warning. The exact installed backend revision was not verified.

## Final verification

All Go package tests and go vet ./... passed after integration. All 125 GUI tests passed. Native Windows tests verified covered-window colors, unchanged foreground, DPI scaling, minimized-content refusal without restore, ambiguity, PID checks, cancellation and child reaping. Local binaries use version 1.0.4-dev to distinguish these tested changes from published v1.0.3.

Both CLI and GUI cross-builds succeeded for Windows amd64, Linux amd64/arm64 and macOS amd64/arm64. Source hashes remained unchanged through compilation. The Windows CLI/GUI executables were replaced with SHA-256 verification; CLI --version and both --help entry points succeeded. The development build is not a new public release.
