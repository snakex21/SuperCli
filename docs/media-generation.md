# Configured image and video generation

`generate_image` and `generate_video` are discoverable tools that produce real
local files. They are disabled until explicitly configured. An ordinary chat
model never implies a media provider, and no failure triggers a paid fallback.
Tool registration and discovery perform no network requests or background work.

## Trust and approval

The application loads media settings only from the user-owned portable
`<dataDir>/config.toml`, using `config.LoadMediaGeneration`. Project
`.supercli/config.toml`, chat-provider settings, model output and tool arguments
cannot select a credential recipient. Treat the global file as trusted
configuration: changing its `base_url` changes where the referenced key is sent.
The generic TOML merge replaces a complete image/video record rather than
combining a new URL with an inherited credential, but application wiring must
still use the global-only loader.

Every execution asks for a real interactive confirmation showing the configured
provider, API root, model, prompt and merged parameters, before the first network
request. An unavailable UI, a rejected prompt or a missing confirmation callback
fails closed. Batch/noninteractive runs therefore cannot silently generate paid
media. Approval is per invocation, not a stored subscription to future requests.
Provider pricing is not inferred or guaranteed: review the selected model's
billing and account limits before approving. There are no automatic resubmissions
if a submission's outcome is uncertain.

## Example configuration

These are opt-in examples, not installed defaults. Set an API key in the named
environment variable outside the config file, verify account access and current
model pricing, then explicitly change the desired `enabled` value to `true`.
No key is discovered from chat configuration, OAuth or unrelated environment
variables. Keys never enter tool schemas, results, URLs or confirmation text.

```toml
[media_generation.image]
enabled = false
provider = "openai"
base_url = "https://api.openai.com/v1"
model = "gpt-image-2.5-flare"
api_key_env = "SUPERCLI_IMAGE_API_KEY"
allowed_parameters = ["size", "quality", "background", "output_format"]
max_bytes = 33554432
# Total deadline starts after interactive approval.
timeout_seconds = 600

[media_generation.image.default_parameters]
size = "1024x1024"
quality = "low"
output_format = "png"

[media_generation.video]
enabled = false
provider = "fal"
base_url = "https://queue.fal.run"
model = "fal-ai/kling-video/v2.5-turbo/pro/text-to-video"
api_key_env = "SUPERCLI_VIDEO_API_KEY"
allowed_parameters = ["duration", "aspect_ratio", "negative_prompt"]
# Exact hostnames only. Add a new host only after independently verifying it.
allowed_download_hosts = ["v3.fal.media", "storage.googleapis.com"]
max_bytes = 33554432
timeout_seconds = 600

[media_generation.video.default_parameters]
duration = "5"
aspect_ratio = "16:9"
```

The API root is joined only with a fixed images path or the configured fal model
path. A model must be an identifier, never a URL. Providers are `openai` for
images and `fal` for video; neither provider, URL, model nor key can be overridden
in a tool call. The example model is a documented text-to-video endpoint; other
fal endpoints must accept a prompt and return a `video.url` result. Input-image,
voice/reference uploads and arbitrary provider-specific request objects are not
supported by these tools.

Minimal tool arguments:

```json
{"prompt":"A small blue bird illustrated on a cream background"}
```

Optional explicitly allowed parameters:

```json
{"prompt":"A bird flies over a quiet lake","parameters":{"duration":"5","aspect_ratio":"16:9"}}
```

Image parameter names supported by the implementation are `size`, `quality`,
`background`, `output_format` (strings), and `output_compression` (integer 0–100).
Video names are `duration`, `aspect_ratio`, `resolution`, `negative_prompt`
(strings), `seed`, `num_frames`, `num_inference_steps` (nonnegative 32-bit
integers), and `generate_audio` (boolean). Only names listed in
`allowed_parameters` can be used, including defaults. Use the selected model's
schema to choose valid values; the provider validates model-specific enums.
Endpoints, reference URLs, credentials, webhooks, streaming flags, image counts
and model selectors cannot be smuggled through parameter defaults or arguments.

## Requests, output and cancellation

Images use `POST /images/generations`, with explicit model, prompt and `n: 1`.
Current GPT image models return `data[0].b64_json`; the legacy `response_format`
parameter is intentionally omitted. URL-only responses are rejected. Successful
PNG, JPEG or WebP data is decoded into a bounded local file.

Videos use one fal queue submission, then one context-bound SSE connection to
`<status_url>/stream` for `IN_QUEUE`, `IN_PROGRESS` and `COMPLETED`. Status
updates arrive over that connection; there is no reconnect or polling fallback. A completed job's `error` still means failure.
The result's `video.url` is downloaded without provider credentials. Queue status,
result and cancellation URLs must have exactly the configured API origin;
foreign origins are rejected before any authenticated request. On cancellation,
timeout or another pre-completion failure after obtaining a usable job ID,
SuperCli attempts one bounded `PUT` to the trusted cancellation URL. This is
best-effort: a running provider job may finish and remain billable. If submission
fails before a usable job ID/URL is returned, its remote outcome can be unknown.
There is no background status connection left after the tool returns.

Outputs are exclusive, random-named files under `<workspace>/generated`, scoped
with `os.Root`; existing files are never overwritten and failed partial writes
are removed. File magic determines the format, not a remote MIME header or file
extension. Supported videos are MP4 and WebM. Each output is limited to 32 MiB
(or a smaller configured `max_bytes`), compatible with the local preview limit.
Image JSON envelopes and video job metadata are separately bounded. The deadline
covers generation, streaming status and saving, with up to three extra seconds
for an independent remote cancellation request. Valid timeout settings are
1–3600 seconds (default 600). Legacy `poll_interval_milliseconds` settings remain
accepted with their previous validation but no longer control video execution.

Successful results are small `show_media`-compatible metadata:

```json
{"type":"video","path":"/workspace/generated/video-unique.mp4","media_type":"video/mp4","bytes":12345,"attached":false}
```

The image/video bytes are never inserted into model history by generation.
The GUI may preview the local file; use `read_image` separately if the model must
inspect a generated image.

All production endpoints require HTTPS. Redirects are rejected. Downloads use
only exact configured CDN hosts and no authentication or cookies. The HTTP
transport disables environment proxies, checks every resolved address, rejects
private/loopback/link-local/special-use addresses, then dials the checked IP
without a second DNS lookup. No credentials follow a provider-returned media URL.
Provider HTTP/error response bodies and signed download URLs are withheld from
errors to avoid exposing keys, private payloads or download tokens.

## Offline verification

The tests use only `httptest` servers and fixture credentials. They cover image
success, authorization refusal, absent config/key, parameter validation, malformed
responses, oversize limits, fal queue progression, completed-job failures,
timeouts, context cancellation, best-effort PUT cancellation, foreign origins,
redirects, unauthenticated downloads, private-network address rejection,
exclusive outputs and symlink confinement. Tests make no paid provider calls.

```sh
go test ./internal/tools/mediagen ./internal/system/config
```

## Official protocol sources

Verified 2026-10-02:

- [OpenAI create-image API](https://developers.openai.com/api/reference/resources/images/methods/generate)
- [fal queue submit, streaming status, result and cancellation](https://fal.ai/docs/documentation/model-apis/inference/queue)
- [Kling 2.5 Turbo Pro text-to-video schema](https://fal.ai/models/fal-ai/kling-video/v2.5-turbo/pro/text-to-video/api)

There is no OpenAI video/Sora adapter in this implementation. The supported video
path is the active, explicitly configured fal queue protocol above.

## TUI confirmation review

Action confirmations show the complete prompt and parameters in a scrollable detail panel. Cancel is selected by default; Enter without explicitly choosing Allow once cancels. Use ↑/↓, PgUp/PgDn, Home/End to inspect all details, then Tab to select Allow once at the end. Esc cancels. Approval is disabled below 24 columns or 10 rows. Control and bidirectional formatting characters are rendered as visible escapes.
