# Images, navigator context and multimodal addons — 2026-09-27

## Confirmed defects

The optional navigator assembled its input directly from canonical history.
Unlike the main request, it did not project dormant image references. The real
OpenAI-compatible serializer therefore reread old image files and sent their
contents again. A missing old file prevented the classification HTTP request
entirely, causing a keyword fallback. The same helper also received the current
prompt twice, native reasoning parts, and up to 500 characters per text part
rather than per historical message.

A separate defect affected the ordinary coordinator path, including when the
navigator was disabled: repo preflight and the existing implementation hint were
appended to Content on an image-bearing message. The serializers use Parts for
multimodal messages, so these additions were silently absent from the request.

Evidence:
- .tmp/navigator-media-history-2026-09-27/red.json
- .tmp/navigator-media-history-2026-09-27/image-context-red.json

## Changes

- Navigator history contains bounded visible dialogue and image labels, without
  image bytes, file reads or native reasoning. Internal tool-image messages and
  task notifications do not displace the recent dialogue.
- The raw current prompt appears once. The existing history budget remains
  three earlier dialogue messages when Run has appended the current prompt.
- New user images go directly to the coordinator. An enabled navigator no longer
  makes a separate model request for that attachment, and a greeting caption
  cannot route an image to the restricted chat context.
- Existing repo context and implementation hints are appended to text Parts for
  multimodal messages. Slice copying protects persisted raw user messages and
  previous request snapshots. Plain-text messages retain their former format.

No new instruction text, helper call, setting, storage location or provider
transport was added. OpenCode Zen transport remains unchanged.

Routing a new attachment directly to the coordinator intentionally restores
project capabilities/history. It does not guarantee a smaller main request than
the former chat route. Restoring a previously lost addon also restores its input
tokens; the point is correct evidence delivery, not hiding that cost.

## Verification and limits

- All eight initial failing cases pass: three historical-image storage cases,
  bounded text/history isolation, and four current-image routing combinations.
- Real HTTP checks cover repo context, existing hints, the user addon, image
  delivery and clean persistence in both Chat Completions and Zen Responses,
  with the navigator enabled and disabled.
- Historical-image fixture request sizes:
  - Inline: 17642 -> 1128 bytes.
  - File-backed: 9450 -> 1128 bytes.
  - Missing file: classification now reaches the endpoint successfully.
- Fixtures use synthetic image payloads to exercise transport; these byte counts
  are not model token billing or a measured whole-session speedup.
- Current-image cases avoid one classification call when model classification
  would previously run. Keyword-only decisions already used zero helper calls.
- Existing real-PNG repository-image tests, native/thin worker controls,
  navigator fallback tests and preflight tests pass.
- Full go test -timeout=90s ./..., go vet ./..., CLI/GUI builds and CLI --help
  smoke all pass. Installed hashes match the builds; old binaries remain in the
  application-local backup directory.

Installed:
- supercli.exe: 3150f725997ad9d2276f40a62e3382a9e7b0d01fae3f00eff6c639baddaf40e0
- supercli-web.exe: 65c34ca1747ab67a5afcb429716cba17944fef935a49423f38a586b9d73e7c7f

## Audit disposition

The earlier light-route tool-image boundary suspicion was not promoted to a
production fix: built-in light-route tools do not normally produce a new image
wrapper, and load_session_image reactivates an existing reference. A raw-history
unit fixture alone would not establish a common affected path. The investigation
instead identified and reproduced the navigator and coordinator defects above.

The broader efficiency goal remains open: task-level duplicate verification in
real coding/delegation sessions still needs representative evidence. This batch
does not claim to eliminate model-driven rereads or repeated successful tests.
